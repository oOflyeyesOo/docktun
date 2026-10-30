package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"docktun/internal/templateprobe"
	"golang.org/x/sys/unix"
)

type TemplateCompatibilityCheck struct {
	Service string `json:"service"`
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type templateFilesystem struct {
	Free     uint64 `json:"free_bytes"`
	Readable bool   `json:"readable"`
	Writable bool   `json:"writable"`
}
type templateImageInfo struct {
	OS           string
	Architecture string
	Variant      string
	Config       struct{ User string }
}

func normalizeTemplateArchitecture(value string) string {
	switch value {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	case "armv7l":
		return "arm/v7"
	case "armv6l":
		return "arm/v6"
	}
	return value
}
func templatePlatformMatches(expected, actual string) bool {
	a, b := strings.Split(expected, "/"), strings.Split(actual, "/")
	if len(a) < 2 || len(b) < 2 || a[0] != b[0] || a[1] != b[1] {
		return false
	}
	return len(a) < 3 || len(b) < 3 || a[2] == b[2]
}
func manifestTemplatePlatforms(raw []byte) []string {
	platforms := []string{}
	seen := map[string]bool{}
	var document any
	if json.Unmarshal(raw, &document) != nil {
		return platforms
	}
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, item := range value {
				visit(item)
			}
		case map[string]any:
			for key, item := range value {
				if strings.EqualFold(key, "platform") {
					if platform, ok := item.(map[string]any); ok {
						osName, architecture, variant := "", "", ""
						for k, v := range platform {
							text, _ := v.(string)
							switch strings.ToLower(k) {
							case "os":
								osName = text
							case "architecture":
								architecture = text
							case "variant":
								variant = text
							}
						}
						if osName != "" && architecture != "" && osName != "unknown" && architecture != "unknown" {
							text := osName + "/" + architecture
							if variant != "" {
								text += "/" + variant
							}
							if !seen[text] {
								platforms = append(platforms, text)
								seen[text] = true
							}
						}
					}
				} else {
					visit(item)
				}
			}
		}
	}
	visit(document)
	return platforms
}

var templateNumericUser = regexp.MustCompile(`^[0-9]+:[0-9]+$`)

func declaredTemplateUser(service map[string]any, imageUser string) (string, []string) {
	user, _ := service["user"].(string)
	if user == "" {
		environment, _ := service["environment"].(map[string]any)
		uid, uok := environment["PUID"].(string)
		gid, gok := environment["PGID"].(string)
		if uok && gok {
			user = uid + ":" + gid
		} else {
			user = imageUser
		}
	}
	if !templateNumericUser.MatchString(user) {
		return "", nil
	}
	parts := strings.Split(user, ":")
	for _, part := range parts {
		value, err := strconv.ParseUint(part, 10, 31)
		if err != nil || value > 2147483647 {
			return "", nil
		}
	}
	groups := []string{}
	if values, ok := service["group_add"].([]any); ok {
		for _, value := range values {
			group := fmt.Sprint(value)
			if _, err := strconv.ParseUint(group, 10, 31); err != nil {
				return "", nil
			}
			groups = append(groups, group)
		}
	}
	return user, groups
}

func checkTemplateCompatibility(ctx context.Context, document map[string]any, own updateContainer, directory, temporary string, staged map[string]string, files map[string]TemplateProjectFile, result *ComposeReview) {
	add := func(service, name, status, message string) {
		result.Checks = append(result.Checks, TemplateCompatibilityCheck{service, name, status, message})
		if status == "failed" {
			result.Problems = append(result.Problems, service+": "+message)
		} else if status == "warning" {
			result.Warnings = append(result.Warnings, service+": "+message)
		}
	}
	var info struct {
		OSType        string
		Architecture  string
		DockerRootDir string
	}
	if err := dockerJSON(ctx, "GET", "/info", nil, &info); err != nil {
		add("Docker host", "Compatibility", "failed", "Cannot inspect Docker host architecture and storage")
		return
	}
	host := info.OSType + "/" + normalizeTemplateArchitecture(info.Architecture)
	add("Docker host", "CPU architecture", "passed", host)
	if info.DockerRootDir != "" {
		raw, err := runTemplateProbe(ctx, own.Image, "filesystem", nil, info.DockerRootDir, "", false, nil)
		var fs templateFilesystem
		if err == nil && json.Unmarshal(raw, &fs) == nil {
			status := "passed"
			if fs.Free < 64<<20 {
				status = "failed"
			} else if fs.Free < 1<<30 {
				status = "warning"
			}
			add("Docker host", "Docker storage", status, fmt.Sprintf("%.1f GiB free for images and Docker volumes", float64(fs.Free)/(1<<30)))
		} else {
			add("Docker host", "Docker storage", "warning", "Docker storage space could not be measured")
		}
	}
	ancestor := directory
	for {
		if _, err := os.Stat(ancestor); err == nil {
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break
		}
		ancestor = parent
	}
	var disk unix.Statfs_t
	if unix.Statfs(ancestor, &disk) == nil {
		free := uint64(disk.Bavail) * uint64(disk.Bsize)
		status := "passed"
		if free < 64<<20 {
			status = "failed"
		} else if free < 1<<30 {
			status = "warning"
		}
		add("Project files", "Disk space", status, fmt.Sprintf("%.1f GiB free on the projects filesystem; app data growth depends on the app", float64(free)/(1<<30)))
	}
	services, _ := document["services"].(map[string]any)
	if len(result.Problems) == 0 {
		request := templateprobe.Request{}
		wanted := map[string]int{}
		owners := map[string]string{}
		groups := map[string]int{}
		for name, entry := range services {
			service, _ := entry.(map[string]any)
			ports, _ := service["ports"].([]any)
			for _, entry := range ports {
				port, _ := entry.(map[string]any)
				published, _ := port["published"].(string)
				number, err := strconv.Atoi(published)
				if err != nil || number <= 0 {
					continue
				}
				protocol, _ := port["protocol"].(string)
				if protocol == "" {
					protocol = "tcp"
				}
				if protocol != "tcp" && protocol != "udp" {
					continue
				}
				ip, _ := port["host_ip"].(string)
				key := name + ":" + ip + ":" + published
				index, exists := groups[key]
				if !exists {
					index = len(request.Groups)
					groups[key] = index
					request.Groups = append(request.Groups, templateprobe.Group{ID: key, Preferred: number})
					wanted[key] = number
					owners[key] = name
				}
				request.Groups[index].Uses = append(request.Groups[index].Uses, templateprobe.Use{IP: ip, Protocol: protocol})
			}
		}
		if len(request.Groups) > 0 {
			raw, err := runTemplateProbe(ctx, own.Image, "ports", request, "", "", false, nil)
			var response templateprobe.Result
			if err != nil || json.Unmarshal(raw, &response) != nil {
				add("Docker host", "Host ports", "warning", "Non-Docker host listeners could not be checked; Docker will validate bindings during deployment")
			} else {
				for id, port := range response.Ports {
					if port != strconv.Itoa(wanted[id]) {
						add(owners[id], "Host ports", "failed", fmt.Sprintf("Host port %d is occupied. Find free host ports suggests %s.", wanted[id], port))
					} else {
						add(owners[id], "Host ports", "passed", fmt.Sprintf("Host port %d is available now", wanted[id]))
					}
				}
				for _, message := range response.Errors {
					add("Docker host", "Host ports", "failed", message)
				}
			}
		}
	}

	type imageResult struct {
		platforms []string
		user      string
		failure   string
		unknown   bool
	}
	images := map[string]imageResult{}
	for name, entry := range services {
		service, _ := entry.(map[string]any)
		image, _ := service["image"].(string)
		environment, _ := service["environment"].(map[string]any)
		if modernPostgresTemplate(image) && environment["PGDATA"] == nil {
			for _, mount := range result.Mounts {
				if mount.Service == name && mount.Target == "/var/lib/postgresql/data" {
					add(name, "Database storage layout", "failed", "PostgreSQL 18+ requires its default data volume at /var/lib/postgresql, not /var/lib/postgresql/data. For a new database, change the mount destination; existing databases require a major-version migration first. An explicit PGDATA setting can select a custom layout.")
				}
			}
		}
		imageUser := ""
		if service["build"] != nil {
			add(name, "Image architecture", "warning", "This service builds from source; Docker validates its build and target platform during deployment")
		} else if image != "" {
			expected := host
			explicit := false
			if platform, ok := service["platform"].(string); ok && platform != "" {
				expected = platform
				explicit = true
			}
			cacheKey := image + "\x00" + expected
			checked, found := images[cacheKey]
			if !found {
				var local templateImageInfo
				inspectPath := "/images/" + url.PathEscape(image) + "/json"
				if explicit {
					inspectPath += "?platform=" + url.QueryEscape(expected)
				}
				if dockerJSON(ctx, "GET", inspectPath, nil, &local) == nil && (!explicit || templatePlatformMatches(expected, local.OS+"/"+local.Architecture)) {
					platform := local.OS + "/" + local.Architecture
					if local.Variant != "" {
						platform += "/" + local.Variant
					}
					checked = imageResult{platforms: []string{platform}, user: local.Config.User}
				} else {
					remoteCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					cmd := exec.CommandContext(remoteCtx, "docker", "manifest", "inspect", "--verbose", image)
					cmd.Env = composeEnvironment()
					raw, err := cmd.CombinedOutput()
					cancel()
					if err != nil {
						message := strings.ToLower(string(raw))
						checked.unknown = true
						checked.failure = "Image metadata could not be verified. Check the image name, registry access and network."
						if strings.Contains(message, "manifest unknown") || strings.Contains(message, "no such manifest") {
							checked.unknown = false
							checked.failure = "Image tag or digest was not found: " + image
						}
					} else {
						checked.platforms = manifestTemplatePlatforms(raw)
						if len(checked.platforms) == 0 {
							checked.unknown = true
							checked.failure = "Registry did not provide usable architecture metadata for " + image
						}
					}
				}
				images[cacheKey] = checked
			}
			imageUser = checked.user
			if checked.failure != "" {
				status := "failed"
				if checked.unknown {
					status = "warning"
				}
				add(name, "Image availability", status, checked.failure)
			} else {
				matches := false
				for _, platform := range checked.platforms {
					if templatePlatformMatches(expected, platform) {
						matches = true
						break
					}
				}
				if !matches {
					add(name, "Image architecture", "failed", image+" does not provide "+expected+"; available: "+strings.Join(checked.platforms, ", "))
				} else if !templatePlatformMatches(host, expected) {
					add(name, "Image architecture", "warning", image+" requests "+expected+" on "+host+"; host emulation must be configured")
				} else {
					add(name, "Image availability & CPU", "passed", image+" is available for "+expected)
				}
			}
		}
		user, groups := declaredTemplateUser(service, imageUser)
		volumes, _ := service["volumes"].([]any)
		volumes = append([]any{}, volumes...)
		for _, kind := range []string{"secrets", "configs"} {
			resources, _ := document[kind].(map[string]any)
			attached, _ := service[kind].([]any)
			for _, entry := range attached {
				attachment, _ := entry.(map[string]any)
				source, _ := attachment["source"].(string)
				target, _ := attachment["target"].(string)
				resource, _ := resources[source].(map[string]any)
				file, _ := resource["file"].(string)
				if file == "" {
					continue
				}
				if target == "" {
					target = source
				}
				if !strings.HasPrefix(target, "/") {
					if kind == "secrets" {
						target = "/run/secrets/" + target
					} else {
						target = "/" + target
					}
				}
				volumes = append(volumes, map[string]any{"type": "bind", "source": file, "target": target, "read_only": true})
			}
		}

		for _, entry := range volumes {
			mount, _ := entry.(map[string]any)
			if mount["type"] != "bind" {
				continue
			}
			source, _ := mount["source"].(string)
			target, _ := mount["target"].(string)
			write := mount["read_only"] != true
			if user == "" {
				add(name, "Storage permissions", "warning", target+": exact app UID:GID is not declared; confirm the app's permission requirements")
				continue
			}
			if relative, ok := staged[filepath.Clean(source)]; ok {
				stat, err := os.Stat(source)
				if err != nil {
					add(name, "Storage permissions", "failed", "Supplied file cannot be inspected: "+relative)
					continue
				}
				system, ok := stat.Sys().(*syscall.Stat_t)
				if !ok {
					continue
				}
				pair := strings.Split(user, ":")
				uid, _ := strconv.Atoi(pair[0])
				gid, _ := strconv.Atoi(pair[1])
				mode := stat.Mode().Perm()
				bits := mode & 7
				if uint32(uid) == system.Uid {
					bits = (mode >> 6) & 7
				} else if uint32(gid) == system.Gid {
					bits = (mode >> 3) & 7
				} else {
					for _, group := range groups {
						value, _ := strconv.Atoi(group)
						if uint32(value) == system.Gid {
							bits |= (mode >> 3) & 7
						}
					}
				}
				if bits&4 == 0 || write && bits&2 == 0 || stat.IsDir() && bits&1 == 0 {
					add(name, "Storage permissions", "failed", fmt.Sprintf("%s is not accessible to app user %s. Set the supporting file's owner UID/GID or choose its existing host source.", relative, user))
				} else {
					add(name, "Storage permissions", "passed", relative+" is accessible to declared app user "+user)
				}
				continue
			}
			hostSource, err := composeHostSource(source, own, filepath.ToSlash(directory))
			if err != nil {
				continue
			}
			raw, err := runTemplateProbe(ctx, own.Image, "filesystem", nil, hostSource, user, write, groups)
			var fs templateFilesystem
			if err != nil || json.Unmarshal(raw, &fs) != nil {
				add(name, "Storage permissions", "warning", target+": permission probe could not run for "+user)
				continue
			}
			if !fs.Readable || write && !fs.Writable {
				add(name, "Storage permissions", "failed", target+" is not accessible with the required permissions for app user "+user)
			} else {
				add(name, "Storage permissions", "passed", target+" is accessible to declared app user "+user)
			}
		}
	}
}
