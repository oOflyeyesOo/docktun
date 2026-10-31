package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var composeRequiredVariable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::\?|\?)`)
var composeVariable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)[^}]*\}`)
var composeBareVariable = regexp.MustCompile(`(^|[^$])\$([A-Za-z_][A-Za-z0-9_]*)`)

// Compose reads files inside DockTun, but Docker interprets bind sources on the
// daemon host. Render locally, translate known mounts, and stream the resolved
// document without rewriting the user's files or persisting expanded secrets.
func composeProjectCommand(projectName, directory string, args ...string) (*exec.Cmd, error) {
	return composeProjectCommandWithFile(projectName, directory, "", args...)
}

func composeProjectCommandWithFile(projectName, directory, source string, args ...string) (*exec.Cmd, error) {
	var sources []string
	if source != "" {
		sources = []string{source}
	}
	return composeProjectCommandWithFiles(projectName, directory, sources, args...)
}

func composeProjectCommandWithFiles(projectName, directory string, sources []string, args ...string) (*exec.Cmd, error) {
	return composeProjectCommandPolicy(projectName, directory, sources, false, args...)
}

func composeProjectCommandPolicy(projectName, directory string, sources []string, strictStorage bool, args ...string) (*exec.Cmd, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	prefix := []string{"compose", "--project-directory", absolute}
	if projectName != "" {
		prefix = append(prefix, "-p", projectName)
	}
	if len(sources) == 0 {
		for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
			candidate := filepath.Join(absolute, name)
			if fileExists(candidate) {
				sources = []string{candidate}
				break
			}
		}
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no Compose file found in %s", absolute)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	renderArgs := append([]string{}, prefix...)
	for _, source := range sources {
		if !fileExists(source) {
			return nil, fmt.Errorf("Compose source %s is unavailable", source)
		}
		renderArgs = append(renderArgs, "-f", source)
	}
	renderArgs = append(renderArgs, "config", "--format", "json")
	composeEnv := composeEnvironmentWithDefaults(sources[0])
	raw, err := composeDockerOutputInDirWithEnv(ctx, absolute, composeEnv, renderArgs...)
	if err != nil {
		return nil, fmt.Errorf("validate Compose configuration: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("invalid Compose configuration response")
	}
	if err := validateComposeReferencedPaths(document, filepath.ToSlash(absolute)); err != nil {
		return nil, err
	}
	if hasComposeBindSources(document) {
		inspector := NewProviderContainers(nil)
		inspector.run = composeDockerOutput
		own, err := inspector.self(ctx)
		if err != nil {
			// Native development uses the daemon host's filesystem directly.
			if _, markerErr := os.Stat("/.dockerenv"); !os.IsNotExist(markerErr) {
				return nil, fmt.Errorf("cannot map Compose bind mounts to the Docker host: %w", err)
			}

		} else if err := remapComposeBindSources(document, own, filepath.ToSlash(absolute)); err != nil {
			return nil, err
		}
	}
	if strictStorage {
		services, _ := document["services"].(map[string]any)
		for _, value := range services {
			service, _ := value.(map[string]any)
			volumes, _ := service["volumes"].([]any)
			for _, value := range volumes {
				volume, _ := value.(map[string]any)
				if volume["type"] == "bind" {
					bind, _ := volume["bind"].(map[string]any)
					if bind == nil {
						bind = map[string]any{}
						volume["bind"] = bind
					}
					bind["create_host_path"] = false
				}
			}
		}
	}
	resolved, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	commandArgs := append(append([]string{}, prefix...), "-f", "-")
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command("docker", commandArgs...)
	cmd.Dir = absolute
	cmd.Env = composeEnv
	cmd.Stdin = bytes.NewReader(resolved)
	return cmd, nil
}

func composeEnvironmentWithDefaults(source string) []string {
	env := composeEnvironment()
	raw, err := os.ReadFile(source)
	if err != nil {
		return env
	}
	existing := make(map[string]bool)
	for _, entry := range env {
		if key, _, ok := strings.Cut(entry, "="); ok {
			existing[key] = true
		}
	}
	for _, match := range composeRequiredVariable.FindAllStringSubmatch(string(raw), -1) {
		key := match[1]
		if existing[key] {
			continue
		}
		value := composeGeneratedVariableValue(key)
		env = append(env, key+"="+value)
		existing[key] = true
	}
	for _, match := range composeVariable.FindAllStringSubmatch(string(raw), -1) {
		key := match[1]
		if existing[key] || strings.Contains(match[0], ":-") || strings.Contains(match[0], ":+") {
			continue
		}
		env = append(env, key+"="+composeGeneratedVariableValue(key))
		existing[key] = true
	}
	for _, match := range composeBareVariable.FindAllStringSubmatch(string(raw), -1) {
		key := match[2]
		if existing[key] {
			continue
		}
		env = append(env, key+"="+composeGeneratedVariableValue(key))
		existing[key] = true
	}
	return env
}

func composeGeneratedVariableValue(key string) string {
	switch strings.ToUpper(key) {
	case "DATA_LOCATION":
		return "./data"
	case "SERVICE":
		return "app"
	case "NETWORK":
		return "docktun"
	}
	lower := strings.ToLower(key)
	if strings.Contains(lower, "url") {
		return "http://localhost"
	}
	if strings.Contains(lower, "email") {
		return "admin@localhost"
	}
	return "docktun-generated"
}

func validateComposeReferencedPaths(document map[string]any, projectDirectory string) error {
	projectDirectory = strings.TrimRight(path.Clean(projectDirectory), "/")
	check := func(kind, value string, wantDir bool) error {
		value = path.Clean(strings.ReplaceAll(value, "$$", "$"))
		if value != projectDirectory && !strings.HasPrefix(value, projectDirectory+"/") {
			return nil
		}
		info, err := os.Stat(filepath.FromSlash(value))
		if err != nil {
			if os.IsNotExist(err) {
				if wantDir {
					if err := os.MkdirAll(filepath.FromSlash(value), 0755); err != nil {
						return fmt.Errorf("cannot create Compose %s %s: %w", kind, value, err)
					}
					return nil
				}
				if err := os.MkdirAll(filepath.Dir(filepath.FromSlash(value)), 0755); err != nil {
					return fmt.Errorf("cannot prepare Compose file %s: %w", kind, err)
				}
				if err := os.WriteFile(filepath.FromSlash(value), composeGeneratedFile(value), 0600); err != nil {
					return fmt.Errorf("cannot create Compose file %s: %w", kind, err)
				}
				return nil
			}
			return fmt.Errorf("cannot inspect Compose %s %s: %w", kind, value, err)
		}

		if wantDir && !info.IsDir() {
			return fmt.Errorf("Compose %s %s must be a directory", kind, value)
		}
		if !wantDir && info.IsDir() {
			return fmt.Errorf("Compose %s %s must be a file", kind, value)
		}
		return nil
	}

	services, _ := document["services"].(map[string]any)
	for serviceName, value := range services {
		service, _ := value.(map[string]any)
		volumes, _ := service["volumes"].([]any)
		for _, value := range volumes {
			volume, _ := value.(map[string]any)
			if volume["type"] != "bind" {
				continue
			}
			source, _ := volume["source"].(string)
			target, _ := volume["target"].(string)
			if err := check(fmt.Sprintf("bind source for service %q", serviceName), source, !composePathLooksLikeFile(source, target)); err != nil {
				return err
			}
		}
		envFiles, _ := service["env_file"].([]any)
		for _, value := range envFiles {
			if file, ok := value.(string); ok {
				if err := check(fmt.Sprintf("env_file for service %q", serviceName), file, false); err != nil {
					return err
				}
			}
		}
		build, _ := service["build"].(map[string]any)
		if context, ok := build["context"].(string); ok {
			if err := check(fmt.Sprintf("build context for service %q", serviceName), context, true); err != nil {
				return err
			}
		}
	}
	for _, kind := range []string{"secrets", "configs"} {
		resources, _ := document[kind].(map[string]any)
		for name, value := range resources {
			resource, _ := value.(map[string]any)
			if file, ok := resource["file"].(string); ok {
				if err := check(fmt.Sprintf("%s %q", kind, name), file, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func composePathLooksLikeFile(source, target string) bool {
	for _, value := range []string{source, target} {
		base := path.Base(strings.TrimRight(value, "/"))
		if strings.Contains(base, ".") {
			return true
		}

		switch strings.ToLower(base) {
		case "caddyfile", "dockerfile", "makefile":
			return true
		}
	}
	return false
}

func composeGeneratedFile(source string) []byte {
	switch strings.ToLower(path.Base(source)) {
	case "caddyfile":
		return []byte(":80 {\n\trespond \"DockTun app is running\"\n}\n")
	case "prometheus.yml", "prometheus.yaml":
		return []byte("global:\n  scrape_interval: 15s\nscrape_configs: []\n")
	default:
		return []byte{}
	}
}

func composeDockerOutput(ctx context.Context, args ...string) ([]byte, error) {
	return composeDockerOutputInDir(ctx, "", args...)
}

func composeDockerOutputInDir(ctx context.Context, directory string, args ...string) ([]byte, error) {
	return composeDockerOutputInDirWithEnv(ctx, directory, nil, args...)
}

func composeDockerOutputInDirWithEnv(ctx context.Context, directory string, environment []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = directory
	if environment == nil {
		environment = composeEnvironment()
	}
	cmd.Env = environment
	out, err := cmd.Output()
	if err != nil {
		detail := err.Error()
		if exit, ok := err.(*exec.ExitError); ok {
			if stderr := strings.TrimSpace(string(exit.Stderr)); stderr != "" {
				detail = stderr
			}
		}
		return nil, fmt.Errorf("Docker %s: %s", args[0], detail)
	}
	return out, nil
}

func hasComposeBindSources(document map[string]any) bool {
	services, _ := document["services"].(map[string]any)
	for _, value := range services {
		service, _ := value.(map[string]any)
		volumes, _ := service["volumes"].([]any)
		for _, value := range volumes {
			volume, _ := value.(map[string]any)
			if volume["type"] == "bind" {
				return true
			}
		}
	}
	for _, kind := range []string{"secrets", "configs"} {
		resources, _ := document[kind].(map[string]any)
		for _, value := range resources {
			resource, _ := value.(map[string]any)
			if file, _ := resource["file"].(string); file != "" {
				return true
			}
		}
	}
	return false
}

func remapComposeBindSources(document map[string]any, own updateContainer, projectDirectory string) error {
	services, _ := document["services"].(map[string]any)
	for _, value := range services {
		service, _ := value.(map[string]any)
		volumes, _ := service["volumes"].([]any)
		for _, value := range volumes {
			volume, _ := value.(map[string]any)
			if volume["type"] != "bind" {
				continue
			}
			source, _ := volume["source"].(string)
			mapped, err := composeHostSource(source, own, projectDirectory)
			if err != nil {
				return err
			}
			volume["source"] = strings.ReplaceAll(mapped, "$", "$$")
		}
		// Standalone Compose implements file-backed secrets/configs as read-only
		// bind mounts too. Express those mounts directly, avoiding client-side file
		// checks against paths that exist only on the daemon host.
		for _, kind := range []string{"secrets", "configs"} {
			references, _ := service[kind].([]any)
			definitions, _ := document[kind].(map[string]any)
			remaining := make([]any, 0)
			for _, value := range references {
				reference, _ := value.(map[string]any)
				name, _ := reference["source"].(string)
				definition, _ := definitions[name].(map[string]any)
				file, _ := definition["file"].(string)
				if file == "" || definition["external"] == true {
					remaining = append(remaining, value)
					continue
				}
				source, err := composeHostSource(file, own, projectDirectory)
				if err != nil {
					return err
				}
				target, _ := reference["target"].(string)
				if target == "" {
					target = name
				}
				if !strings.HasPrefix(target, "/") {
					base := "/"
					if kind == "secrets" {
						base = "/run/secrets"
					}
					target = path.Join(base, target)
				}
				exists := false
				for _, value := range volumes {
					volume, _ := value.(map[string]any)
					if volume["target"] == target {
						exists = true
						break
					}
				}
				if !exists {
					volumes = append(volumes, map[string]any{"type": "bind", "source": strings.ReplaceAll(source, "$", "$$"), "target": target, "read_only": true, "bind": map[string]any{"create_host_path": false}})
				}
			}
			if len(remaining) == 0 {
				delete(service, kind)
			} else {
				service[kind] = remaining
			}
		}
		if len(volumes) > 0 {
			service["volumes"] = volumes
		}
	}
	return nil
}

func composeHostSource(source string, own updateContainer, projectDirectory string) (string, error) {
	// config output escapes literal dollars for another Compose read.
	source = path.Clean(strings.ReplaceAll(source, "$$", "$"))
	best := -1
	for i, mount := range own.Mounts {
		destination := path.Clean(mount.Destination)
		if source == destination || strings.HasPrefix(source, destination+"/") || destination == "/" && strings.HasPrefix(source, "/") {
			if best < 0 || len(destination) > len(path.Clean(own.Mounts[best].Destination)) {
				best = i
			}
		}
	}
	if best < 0 {
		if mapped, ok := composePortainerHostSource(source, own, projectDirectory); ok {
			return mapped, nil
		}
		if source == projectDirectory || strings.HasPrefix(source, strings.TrimRight(projectDirectory, "/")+"/") {
			return "", fmt.Errorf("project bind source %s is not in a persistent DockTun mount; mount the projects folder before deploying", source)
		}
		return source, nil
	}
	mount := own.Mounts[best]
	if mount.Source == "" || (mount.Type != "bind" && mount.Type != "volume") {
		return "", fmt.Errorf("Docker did not report a persistent host source for %s", source)
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(source, path.Clean(mount.Destination)), "/")
	return path.Join(mount.Source, relative), nil
}

func composePortainerHostSource(source string, own updateContainer, projectDirectory string) (string, bool) {
	source = path.Clean(source)
	relative := ""
	for _, prefix := range []string{"/srv/lsio", "/portainer"} {
		if source == prefix || strings.HasPrefix(source, prefix+"/") {
			relative = strings.TrimPrefix(strings.TrimPrefix(source, prefix), "/")
			break
		}
	}
	if relative == "" {
		return "", false
	}
	projectHost, ok := composeProjectHostSource(own, projectDirectory)
	if !ok {
		return "", false
	}
	localPath := path.Join(projectDirectory, ".docktun", "portainer", relative)
	if err := os.MkdirAll(filepath.FromSlash(localPath), 0755); err != nil {
		return "", false
	}
	return path.Join(projectHost, ".docktun", "portainer", relative), true
}

func composeProjectHostSource(own updateContainer, projectDirectory string) (string, bool) {
	for _, mount := range own.Mounts {
		destination := path.Clean(mount.Destination)
		if projectDirectory == destination || strings.HasPrefix(projectDirectory, destination+"/") {
			if mount.Source == "" || (mount.Type != "bind" && mount.Type != "volume") {
				return "", false
			}
			relative := strings.TrimPrefix(strings.TrimPrefix(projectDirectory, destination), "/")
			return path.Join(mount.Source, relative), true
		}
	}
	return "", false
}
