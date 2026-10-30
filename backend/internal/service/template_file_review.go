package service

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

func prepareTemplateFileReview(input ComposeReviewInput, directory, temporary string, document map[string]any) (string, map[string]string, error) {
	decoded, err := decodeTemplateFiles(input.Files)
	if err != nil {
		return "", nil, err
	}
	existingFiles := map[string]os.FileInfo{}
	if root, err := os.OpenRoot(directory); err == nil {
		defer root.Close()
		for name, data := range decoded {
			existing, err := root.ReadFile(name)
			if err == nil && !bytes.Equal(existing, data) {
				return "", nil, fmt.Errorf("supporting file %s already exists with different contents; it will not be overwritten", name)
			}
			if err != nil && !os.IsNotExist(err) {
				return "", nil, fmt.Errorf("cannot safely access existing supporting file %s", name)
			}
			if err == nil {
				info, statErr := root.Stat(name)
				if statErr != nil {
					return "", nil, statErr
				}
				if input.Files[name].Secret && info.Mode().Perm()&0077 != 0 {
					return "", nil, fmt.Errorf("existing secret file %s is accessible to other users; restrict its permissions before reusing it", name)
				}
				existingFiles[name] = info
			}
		}
	} else if !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("cannot safely access project files: %w", err)
	}
	staged, err := writeStagedTemplateFiles(temporary, input.Files)
	if err != nil {
		return "", nil, err
	}
	for target, relative := range staged {
		info, err := os.Stat(target)
		if err != nil {
			continue
		}
		mode := info.Mode().Perm()
		existing := existingFiles[relative]
		if info.IsDir() {
			mode = 0755
			if directoryInfo, err := os.Stat(filepath.Join(directory, relative)); err == nil && directoryInfo.IsDir() {
				existing = directoryInfo
			}
		}
		if existing != nil {
			mode = existing.Mode().Perm()
			if stat, ok := existing.Sys().(*syscall.Stat_t); ok {
				if err = os.Chown(target, int(stat.Uid), int(stat.Gid)); err != nil {
					return "", nil, err
				}
			}
		}
		if err = os.Chmod(target, mode); err != nil {
			return "", nil, err
		}
	}

	environment, envErr := setupEnv(input.Env)
	relocate := func(value string) string {
		if envErr == nil {
			if resolved, err := resolveTemplatePath(value, environment); err == nil {
				value = resolved
			}
		}
		absolute := value
		if !filepath.IsAbs(value) {
			absolute = filepath.Join(directory, value)
		}
		rel, err := filepath.Rel(directory, absolute)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return value
		}
		if _, ok := staged[filepath.Join(temporary, rel)]; ok {
			return filepath.Join(temporary, rel)
		}
		return value
	}
	for _, kind := range []string{"secrets", "configs"} {
		resources, _ := document[kind].(map[string]any)
		for _, entry := range resources {
			resource, _ := entry.(map[string]any)
			if file, ok := resource["file"].(string); ok {
				resource["file"] = relocate(file)
			}
		}
	}
	services, _ := document["services"].(map[string]any)
	for _, entry := range services {
		service, _ := entry.(map[string]any)
		if file, ok := service["env_file"].(string); ok {
			service["env_file"] = relocate(file)
		} else if files, ok := service["env_file"].([]any); ok {
			for i, entry := range files {
				if file, ok := entry.(string); ok {
					files[i] = relocate(file)
				} else if file, ok := entry.(map[string]any); ok {
					if name, ok := file["path"].(string); ok {
						file["path"] = relocate(name)
					}
				}
			}
		}
		if mounts, ok := service["volumes"].([]any); ok {
			for i, entry := range mounts {
				if mount, ok := entry.(string); ok {
					parts := setupMountParts(mount)
					if len(parts) >= 2 {
						replacement := relocate(parts[0])
						if replacement != parts[0] {
							parts[0] = replacement
							mounts[i] = strings.Join(parts, ":")
						}
					}
				} else if mount, ok := entry.(map[string]any); ok && mount["type"] == "bind" {
					source, _ := mount["source"].(string)
					mount["source"] = relocate(source)
				}
			}
		}
		var build map[string]any
		if value, ok := service["build"].(string); ok {
			build = map[string]any{"context": value}
			service["build"] = build
		} else {
			build, _ = service["build"].(map[string]any)
		}
		if build != nil {
			contextPath, _ := build["context"].(string)
			if contextPath == "" {
				contextPath = "."
			}
			if envErr == nil {
				contextPath, _ = resolveTemplatePath(contextPath, environment)
			}
			if !filepath.IsAbs(contextPath) && !strings.Contains(contextPath, "://") {
				clean := filepath.Clean(contextPath)
				if clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
					prefix := filepath.ToSlash(clean)
					for name := range decoded {
						if prefix == "." || strings.HasPrefix(name, prefix+"/") {
							build["context"] = filepath.Join(temporary, clean)
							break
						}
					}
				}
			}
		}
	}
	if len(decoded) == 0 {
		return input.Compose, staged, nil
	}
	raw, err := yaml.Marshal(document)
	return string(raw), staged, err
}
