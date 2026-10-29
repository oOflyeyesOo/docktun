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
	"strings"
	"time"
)

// Compose reads files inside DockTun, but Docker interprets bind sources on the
// daemon host. Render locally, translate known mounts, and stream the resolved
// document without rewriting the user's files or persisting expanded secrets.
func composeProjectCommand(projectName, directory string, args ...string) (*exec.Cmd, error) {
	return composeProjectCommandWithFile(projectName, directory, "", args...)
}

func composeProjectCommandWithFile(projectName, directory, source string, args ...string) (*exec.Cmd, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	prefix := []string{"compose", "--project-directory", absolute}
	if projectName != "" {
		prefix = append(prefix, "-p", projectName)
	}
	if source == "" {
		for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
			candidate := filepath.Join(absolute, name)
			if fileExists(candidate) {
				source = candidate
				break
			}
		}
	}
	if source == "" || !fileExists(source) {
		return nil, fmt.Errorf("no Compose file found in %s", absolute)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	renderArgs := append(append([]string{}, prefix...), "-f", source, "config", "--format", "json")
	raw, err := composeDockerOutputInDir(ctx, absolute, renderArgs...)
	if err != nil {
		return nil, fmt.Errorf("validate Compose configuration: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("invalid Compose configuration response")
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
	resolved, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	commandArgs := append(append([]string{}, prefix...), "-f", "-")
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command("docker", commandArgs...)
	cmd.Dir = absolute
	cmd.Stdin = bytes.NewReader(resolved)
	return cmd, nil
}

func composeDockerOutput(ctx context.Context, args ...string) ([]byte, error) {
	return composeDockerOutputInDir(ctx, "", args...)
}

func composeDockerOutputInDir(ctx context.Context, directory string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = directory
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
