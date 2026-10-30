package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"docktun/internal/db"
	"gopkg.in/yaml.v3"
)

func (s *Projects) MarkStorageReviewed(id int, source string) error {
	_, err := s.db.Exec(`INSERT INTO project_storage_reviews(project_id,source) VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET source=excluded.source`, id, source)
	return err
}
func reviewedProject(database *db.DB, id int) bool {
	if database == nil || id == 0 {
		return false
	}
	var exists int
	err := database.QueryRow(`SELECT 1 FROM project_storage_reviews WHERE project_id=?`, id).Scan(&exists)
	// A failed policy read must not permit implicit host directory creation.
	return err == nil || !errors.Is(err, sql.ErrNoRows)
}

type ComposeReviewInput struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Compose string `json:"compose_content"`
	Env     string `json:"env_content"`
}
type ReviewedMount struct {
	Service string `json:"service"`
	Type    string `json:"type"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	Access  string `json:"access"`
	Status  string `json:"status"`
}
type ComposeReview struct {
	Ready     bool            `json:"ready"`
	Directory string          `json:"directory"`
	Mounts    []ReviewedMount `json:"mounts"`
	Problems  []string        `json:"problems"`
}

// App credentials must never become interpolation values for imported stacks.
// The project's own .env remains available to Compose in its working directory.
func composeEnvironment() []string {
	result := []string{}
	for _, key := range []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_TLS", "DOCKER_API_VERSION", "DOCKER_DEFAULT_PLATFORM", "DOCKER_BUILDKIT"} {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	return result
}

var placeholderMount = regexp.MustCompile(`(?i)(/path/(to|on/host)(/|$)|/your/(path|folder|data|media)(/|$)|replace[_-]?me|change[_-]?me|<[^>]+>)`)

func ReviewCompose(ctx context.Context, input ComposeReviewInput) (ComposeReview, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result := ComposeReview{Mounts: []ReviewedMount{}, Problems: []string{}}
	input.Name, input.Path = strings.TrimSpace(input.Name), strings.TrimSpace(input.Path)
	if input.Name == "" || len(input.Compose) > 1024*1024 || len(input.Env) > 128*1024 {
		return result, fmt.Errorf("provide a stack name and Compose file up to 1 MiB; .env is limited to 128 KiB")
	}
	if input.Path == "" {
		input.Path = filepath.Join(ProjectsRoot(), input.Name)
	}
	var err error
	result.Directory, err = filepath.Abs(input.Path)
	if err != nil {
		return result, err
	}
	own, err := selfContainer(ctx)
	if err != nil {
		return result, fmt.Errorf("cannot check storage on the Docker host: %w", err)
	}
	if mapped, found, mapErr := projectHostDirectory(filepath.ToSlash(result.Directory), own); mapErr != nil {
		return result, mapErr
	} else if found {
		result.Directory = mapped.Path
	}
	best := -1
	for i, mount := range own.Mounts {
		if pathWithin(filepath.ToSlash(result.Directory), path.Clean(mount.Destination)) && (best < 0 || len(mount.Destination) > len(own.Mounts[best].Destination)) {
			best = i
		}
	}
	if best >= 0 && !own.Mounts[best].RW {
		result.Problems = append(result.Problems, "The stack folder is read-only inside DockTun; choose a writable projects folder.")
		return result, nil
	}
	if best < 0 || (own.Mounts[best].Type != "bind" && own.Mounts[best].Type != "volume") || own.Mounts[best].Source == "" {
		result.Problems = append(result.Problems, "Choose a stack folder inside a persistent DockTun mount, such as its projects folder.")
		return result, nil
	}
	var document map[string]any
	if yaml.Unmarshal([]byte(input.Compose), &document) != nil {
		return result, fmt.Errorf("invalid Compose YAML")
	}
	if document["include"] != nil {
		result.Problems = append(result.Problems, "This Compose file includes other files. Import the complete project before deploying.")
		return result, nil
	}
	services, _ := document["services"].(map[string]any)
	if len(services) == 0 {
		return result, fmt.Errorf("Compose must contain services")
	}
	for name, value := range services {
		service, _ := value.(map[string]any)
		if service["extends"] != nil {
			result.Problems = append(result.Problems, name+": resolve the additional extends file before deploying this Compose-only import.")
		}
	}
	if len(result.Problems) > 0 {
		return result, nil
	}
	temporary, err := os.MkdirTemp("", "docktun-compose-review-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(temporary)
	for name, content := range map[string]string{"compose.yml": input.Compose, ".env": input.Env} {
		if err = os.WriteFile(filepath.Join(temporary, name), []byte(content), 0600); err != nil {
			return result, err
		}
	}
	render := exec.CommandContext(ctx, "docker", "compose", "--project-directory", result.Directory, "--env-file", filepath.Join(temporary, ".env"), "-p", input.Name, "-f", filepath.Join(temporary, "compose.yml"), "config", "--no-env-resolution", "--format", "json")
	render.Env = composeEnvironment()
	var diagnostics bytes.Buffer
	render.Stderr = &diagnostics
	raw, err := render.Output()
	if err != nil {
		message := "Compose validation failed"
		if diagnostics.Len() > 0 {
			message = strings.TrimSpace(diagnostics.String())
			if len(message) > 2000 {
				message = message[:2000]
			}
		}
		result.Problems = append(result.Problems, message)
		return result, nil
	}
	if strings.Contains(strings.ToLower(diagnostics.String()), "variable is not set") {
		message := strings.TrimSpace(diagnostics.String())
		if len(message) > 2000 {
			message = message[:2000]
		}
		result.Problems = append(result.Problems, "Set the missing variables in this stack's .env: "+message)
		return result, nil
	}
	if json.Unmarshal(raw, &document) != nil {
		return result, fmt.Errorf("invalid normalized Compose response")
	}
	services, _ = document["services"].(map[string]any)
	filters, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + input.Name}})
	var existing []struct {
		ID string `json:"Id"`
	}
	if err := dockerJSON(ctx, "GET", "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), nil, &existing); err != nil {
		return result, fmt.Errorf("cannot check existing Compose projects: %w", err)
	} else if len(existing) > 0 {
		result.Problems = append(result.Problems, "This Compose project already has containers. Manage the existing stack or choose a different stack name.")
		return result, nil
	}
	probes := map[string]string{}
	var running []struct {
		Names []string `json:"Names"`
		Ports []struct {
			IP         string
			PublicPort int
			Type       string
		} `json:"Ports"`
	}
	if err := dockerJSON(ctx, "GET", "/containers/json", nil, &running); err != nil {
		return result, fmt.Errorf("cannot check published ports: %w", err)
	}
	chosenPorts := map[string][]string{}
	for name, value := range services {
		service, _ := value.(map[string]any)
		ports, _ := service["ports"].([]any)
		for _, value := range ports {
			port, _ := value.(map[string]any)
			published, _ := port["published"].(string)
			number, err := strconv.Atoi(published)
			if err != nil || number == 0 {
				continue
			} // Advanced ranges are validated by Docker.
			protocol, _ := port["protocol"].(string)
			if protocol == "" {
				protocol = "tcp"
			}
			ip, _ := port["host_ip"].(string)
			conflicts := func(other string) bool {
				return ip == "" || ip == "0.0.0.0" || ip == "::" || other == "" || other == "0.0.0.0" || other == "::" || other == ip
			}
			for _, container := range running {
				for _, existing := range container.Ports {
					if existing.PublicPort == number && existing.Type == protocol && conflicts(existing.IP) {
						result.Problems = append(result.Problems, fmt.Sprintf("%s: host port %d/%s is already used by %s; choose another host port in app setup", name, number, protocol, strings.Join(container.Names, ", ")))
					}
				}
			}
			key := strconv.Itoa(number) + "/" + protocol
			for _, previous := range chosenPorts[key] {
				if conflicts(previous) {
					result.Problems = append(result.Problems, fmt.Sprintf("%s: host port %s is also published by another service; choose different host ports", name, key))
					break
				}
			}
			chosenPorts[key] = append(chosenPorts[key], ip)
		}
		if explicitName, ok := service["container_name"].(string); ok && explicitName != "" {
			if _, err := inspectUpdateContainer(ctx, explicitName); err == nil {
				result.Problems = append(result.Problems, name+": container name "+explicitName+" is already in use; change container_name in the editor")
			}
		}
		if build, ok := service["build"].(map[string]any); ok {
			contextPath, _ := build["context"].(string)
			row := ReviewedMount{Service: name, Type: "build context", Source: contextPath, Target: "image build", Access: "read by Docker", Status: "repository files available"}
			if strings.Contains(contextPath, "://") || strings.HasPrefix(contextPath, "git@") {
				row.Status = "Docker downloads this remote build context"
			} else {
				inline, _ := build["dockerfile_inline"].(string)
				dockerfile, _ := build["dockerfile"].(string)
				if dockerfile == "" {
					dockerfile = "Dockerfile"
				}
				if inline != "" && contextPath == result.Directory {
					row.Status = "inline Dockerfile; stack folder is created on deployment"
				} else {
					info, err := os.Stat(contextPath)
					if err != nil || !info.IsDir() {
						row.Status = "repository build context is unavailable inside DockTun"
					} else if inline == "" {
						if !filepath.IsAbs(dockerfile) {
							dockerfile = filepath.Join(contextPath, dockerfile)
						}
						if _, err := os.Stat(dockerfile); err != nil {
							row.Status = "repository Dockerfile is unavailable inside DockTun"
						}
					}
					if strings.Contains(row.Status, "unavailable") {
						result.Problems = append(result.Problems, name+": "+row.Status+"; import the complete source project or select a published image")
					}
				}
			}
			result.Mounts = append(result.Mounts, row)
		}
		if envFiles, ok := service["env_file"].([]any); ok {
			for _, entry := range envFiles {
				file, _ := entry.(map[string]any)
				source, _ := file["path"].(string)
				if source == filepath.Join(result.Directory, ".env") && input.Env != "" {
					continue
				}
				if _, err := os.Stat(source); err != nil {
					if os.IsNotExist(err) && file["required"] == false {
						continue
					}
					result.Problems = append(result.Problems, name+": required environment file is unavailable inside DockTun: "+source)
				}
			}
		}
		volumes, _ := service["volumes"].([]any)
		for _, value := range volumes {
			if ctx.Err() != nil {
				result.Problems = append(result.Problems, "Storage review timed out; retry after checking Docker access.")
				return result, nil
			}
			volume, _ := value.(map[string]any)
			kind, _ := volume["type"].(string)
			source, _ := volume["source"].(string)
			target, _ := volume["target"].(string)
			row := ReviewedMount{Service: name, Type: kind, Source: source, Target: target, Access: "read/write", Status: "managed by Docker"}
			if volume["read_only"] == true {
				row.Access = "read-only"
			}
			if kind == "bind" {
				host, mapErr := composeHostSource(source, own, filepath.ToSlash(result.Directory))
				if mapErr != nil {
					row.Status = mapErr.Error()
				} else if placeholderMount.MatchString(host) {
					row.Status = "replace this placeholder host path"
				} else {
					row.Source = host
					if status, exists := probes[host]; exists {
						row.Status = status
					} else {
						row.Status = probeTemplateMount(ctx, own.Image, host)
						probes[host] = row.Status
					}
				}
				if !strings.HasPrefix(row.Status, "exists:") {
					result.Problems = append(result.Problems, name+": "+row.Source+": "+row.Status)
				}
			}
			if kind == "volume" {
				definitions, _ := document["volumes"].(map[string]any)
				definition, _ := definitions[source].(map[string]any)
				if definition["external"] == true {
					actual, _ := definition["name"].(string)
					if actual == "" {
						actual = source
					}
					if _, err := updateDocker(ctx, "volume", "inspect", actual); err != nil {
						row.Status = "external volume is missing"
						result.Problems = append(result.Problems, name+": external volume "+actual+" must exist")
					} else {
						row.Status = "external volume exists"
					}
				} else if source == "" {
					row.Status = "Docker creates an anonymous volume"
				} else {
					row.Status = "Docker creates or reuses this named volume"
				}
			}
			result.Mounts = append(result.Mounts, row)
		}
	}
	for _, kind := range []string{"configs", "secrets"} {
		resources, _ := document[kind].(map[string]any)
		for name, value := range resources {
			resource, _ := value.(map[string]any)
			file, _ := resource["file"].(string)
			if file != "" {
				row := ReviewedMount{Service: "shared " + kind, Type: "file", Source: file, Target: name, Access: "read-only", Status: "file available to Compose"}
				if _, err := os.Stat(file); err != nil {
					row.Status = "required file is unavailable inside DockTun"
					result.Problems = append(result.Problems, kind+" "+name+": "+row.Status+": "+file)
				}
				if host, err := composeHostSource(file, own, filepath.ToSlash(result.Directory)); err == nil {
					row.Source = host
				} else {
					row.Status = err.Error()
					result.Problems = append(result.Problems, row.Status)
				}
				result.Mounts = append(result.Mounts, row)
			}
		}
	}
	result.Ready = len(result.Problems) == 0
	return result, nil
}

func probeTemplateMount(ctx context.Context, image, source string) string {
	if !filepath.IsAbs(source) || strings.ContainsAny(source, "\x00\r\n") {
		return "use an absolute Docker host path without control characters"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	name := fmt.Sprintf("docktun-storage-check-%d", time.Now().UnixNano())
	completed := false
	defer func() {
		if completed {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", name).Run()
	}()
	var mount strings.Builder
	writer := csv.NewWriter(&mount)
	_ = writer.Write([]string{"type=bind", "source=" + source, "target=/checked", "readonly"})
	writer.Flush()
	cmd := exec.CommandContext(probeCtx, "docker", "run", "--rm", "--name", name, "--label", "docktun.storage-check=true", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--mount", strings.TrimSuffix(mount.String(), "\n"), "--entrypoint", "/bin/sh", image, "-c", "stat -c '%F; permissions %a; owner %u:%g' /checked")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "bind source path does not exist") {
			return "host path does not exist; choose an existing folder/file or create the intended folder on the host first"
		}
		return "Docker could not verify this host path; check Docker access and mount permissions"
	}
	completed = true
	return "exists: " + strings.TrimSpace(string(out))
}
