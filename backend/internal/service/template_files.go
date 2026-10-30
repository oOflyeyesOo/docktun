package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type TemplateProjectFile struct {
	Content    string `json:"content"`
	Encoding   string `json:"encoding,omitempty"`
	Secret     bool   `json:"secret,omitempty"`
	Executable bool   `json:"executable,omitempty"`
	OwnerUID   *int   `json:"owner_uid,omitempty"`
	OwnerGID   *int   `json:"owner_gid,omitempty"`
	Source     string `json:"source,omitempty"`
	Sample     bool   `json:"sample,omitempty"`
}
type TemplateFileRequirement struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	Note     string `json:"note"`
}
type TemplateFilesInput struct {
	Compose string `json:"compose_content"`
	Env     string `json:"env_content"`
	Source  string `json:"source_url"`
	Fetch   bool   `json:"fetch"`
}
type TemplateFilesResult struct {
	Requirements     []TemplateFileRequirement      `json:"requirements"`
	Files            map[string]TemplateProjectFile `json:"files"`
	EnvExample       string                         `json:"env_example,omitempty"`
	EnvExampleSource string                         `json:"env_example_source,omitempty"`
	Source           string                         `json:"source_url"`
	Notes            []string                       `json:"notes"`
}

func templateFilePath(raw string) (string, error) {
	clean := path.Clean(strings.TrimPrefix(raw, "./"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.ContainsAny(clean, "\\\x00\r\n:") {
		return "", fmt.Errorf("supporting files must stay inside the project folder")
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("Git metadata cannot be imported as supporting files")
		}
	}
	switch strings.ToLower(clean) {
	case ".env", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml":
		return "", fmt.Errorf("use the Compose and Environment editors for %s", clean)
	}
	return clean, nil
}

func decodeTemplateFiles(files map[string]TemplateProjectFile) (map[string][]byte, error) {
	if len(files) > 128 {
		return nil, fmt.Errorf("supporting files are limited to 128 files")
	}
	decoded := map[string][]byte{}
	total := 0
	for name, file := range files {
		clean, err := templateFilePath(name)
		if err != nil || clean != name {
			return nil, fmt.Errorf("invalid supporting file path %q", name)
		}
		if file.OwnerUID != nil && (*file.OwnerUID < 0 || *file.OwnerUID > 2147483647) || file.OwnerGID != nil && (*file.OwnerGID < 0 || *file.OwnerGID > 2147483647) {
			return nil, fmt.Errorf("invalid file owner ID")
		}
		var data []byte
		switch file.Encoding {
		case "", "utf8":
			data = []byte(file.Content)
		case "base64":
			data, err = base64.StdEncoding.DecodeString(file.Content)
		default:
			err = fmt.Errorf("unsupported encoding")
		}
		if err != nil || len(data) > 256<<10 {
			return nil, fmt.Errorf("supporting file %s is invalid or exceeds 256 KiB", name)
		}
		if file.Secret && len(bytes.TrimSpace(data)) == 0 {
			return nil, fmt.Errorf("provide a value for secret file %s", name)
		}
		total += len(data)
		if total > 4<<20 {
			return nil, fmt.Errorf("supporting files exceed 4 MiB")
		}
		decoded[name] = data
	}
	return decoded, nil
}

func writeStagedTemplateFiles(directory string, files map[string]TemplateProjectFile) (map[string]string, error) {
	decoded, err := decodeTemplateFiles(files)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for name, data := range decoded {
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		spec := files[name]
		mode := os.FileMode(0644)
		if spec.Executable {
			mode = 0755
		}
		if spec.Secret {
			mode = 0600
		}
		if err = os.WriteFile(target, data, mode); err != nil {
			return nil, err
		}
		uid, gid := -1, -1
		if spec.OwnerUID != nil {
			uid = *spec.OwnerUID
		}
		if spec.OwnerGID != nil {
			gid = *spec.OwnerGID
		}
		if uid >= 0 || gid >= 0 {
			if err = os.Chown(target, uid, gid); err != nil {
				return nil, fmt.Errorf("cannot set file owner for %s", name)
			}
		}
		paths[filepath.Clean(target)] = name
		for parent := filepath.Dir(target); parent != directory && parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
			relative, _ := filepath.Rel(directory, parent)
			paths[filepath.Clean(parent)] = filepath.ToSlash(relative)
		}
		paths[filepath.Clean(directory)] = "."

	}
	return paths, nil
}

func (s *Projects) SaveTemplateFiles(id int, files map[string]TemplateProjectFile) error {
	if len(files) == 0 {
		return nil
	}
	decoded, err := decodeTemplateFiles(files)
	if err != nil {
		return err
	}
	root, err := s.projectRoot(id, true)
	if err != nil {
		return err
	}
	defer root.Close()
	for name, data := range decoded {
		if err = root.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if files[name].Executable {
			mode = 0755
		}
		if files[name].Secret {
			mode = 0600
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if os.IsExist(err) {
			existing, readErr := root.ReadFile(name)
			if readErr == nil && bytes.Equal(existing, data) {
				info, statErr := root.Stat(name)
				if statErr != nil {
					return statErr
				}
				if files[name].Secret && info.Mode().Perm()&0077 != 0 {
					return fmt.Errorf("existing secret file %s is accessible to other users; restrict its permissions before reusing it", name)
				}
				continue
			}
			return fmt.Errorf("supporting file already exists: %s; existing data was not overwritten", name)
		}
		if err != nil {
			return err
		}
		uid, gid := -1, -1
		if files[name].OwnerUID != nil {
			uid = *files[name].OwnerUID
		}
		if files[name].OwnerGID != nil {
			gid = *files[name].OwnerGID
		}
		if uid >= 0 || gid >= 0 {
			err = file.Chown(uid, gid)
		}
		if err == nil {
			_, err = file.Write(data)
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func resolveTemplatePath(value string, environment map[string]string) (string, error) {
	missing := false
	value = setupInterpolation.ReplaceAllStringFunc(value, func(token string) string {
		if token == "$$" {
			return "$"
		}
		parts := setupInterpolation.FindStringSubmatch(token)
		name := parts[1]
		if name == "" {
			name = parts[4]
		}
		current, ok := environment[name]
		if (!ok || current == "") && parts[2] == ":-" || !ok && parts[2] == "-" {
			return parts[3]
		}
		if !ok {
			missing = true
		}
		return current
	})
	if missing || strings.Contains(value, "${") {
		return "", fmt.Errorf("fill the file path's Environment variable first")
	}
	return value, nil
}

func planTemplateFiles(input TemplateFilesInput) (TemplateFilesResult, []string, error) {
	result := TemplateFilesResult{Requirements: []TemplateFileRequirement{}, Files: map[string]TemplateProjectFile{}, Notes: []string{}}
	if len(input.Compose) > 1<<20 || len(input.Env) > 128<<10 {
		return result, nil, fmt.Errorf("template exceeds setup limits")
	}
	var doc map[string]any
	if yaml.Unmarshal([]byte(input.Compose), &doc) != nil {
		return result, nil, fmt.Errorf("invalid Compose YAML")
	}
	env, err := setupEnv(input.Env)
	if err != nil {
		return result, nil, err
	}
	requirements := map[string]TemplateFileRequirement{}
	directories := []string{}
	add := func(raw, kind string, secret, required bool) {
		if raw == ".env" || raw == "./.env" {
			requirements[".env"] = TemplateFileRequirement{Path: ".env", Kind: "stack environment", Secret: true, Required: required, Note: "Use the app settings form or Environment editor for these values."}
			return
		}
		resolved, err := resolveTemplatePath(raw, env)
		if err != nil {
			result.Notes = append(result.Notes, raw+": "+err.Error())
			return
		}
		clean, err := templateFilePath(resolved)
		if err != nil {
			result.Notes = append(result.Notes, raw+": "+err.Error()+"; select its existing host path or use a project-relative file")
			return
		}
		existing := requirements[clean]
		requirements[clean] = TemplateFileRequirement{Path: clean, Kind: kind, Secret: secret || existing.Secret, Required: required || existing.Required}
	}
	for _, kind := range []string{"secrets", "configs"} {
		entries, _ := doc[kind].(map[string]any)
		for _, entry := range entries {
			resource, _ := entry.(map[string]any)
			if file, ok := resource["file"].(string); ok {
				add(file, kind, kind == "secrets", true)
			}
		}
	}
	services, _ := doc["services"].(map[string]any)
	for name, entry := range services {
		service, _ := entry.(map[string]any)
		if service["extends"] != nil {
			result.Notes = append(result.Notes, name+": extends requires a complete source project; it is not flattened by file import")
		}
		if files, ok := service["env_file"].(string); ok {
			add(files, "environment", true, true)
		} else if files, ok := service["env_file"].([]any); ok {
			for _, entry := range files {
				if file, ok := entry.(string); ok {
					add(file, "environment", true, true)
				} else if file, ok := entry.(map[string]any); ok {
					value, _ := file["path"].(string)
					add(value, "environment", true, file["required"] != false)
				}
			}
		}
		if mounts, ok := service["volumes"].([]any); ok {
			for _, entry := range mounts {
				source, target := "", ""
				if mount, ok := entry.(string); ok {
					parts := setupMountParts(mount)
					if len(parts) >= 2 {
						source, target = parts[0], parts[1]
					}
				} else if mount, ok := entry.(map[string]any); ok && mount["type"] == "bind" {
					source, _ = mount["source"].(string)
					target, _ = mount["target"].(string)
				}
				if source != "" && !path.IsAbs(source) && (strings.HasPrefix(source, ".") || strings.Contains(source, "/")) && (path.Ext(target) != "" || path.Ext(source) != "") && !strings.HasSuffix(target, ".sock") {
					kind := "configuration"
					if strings.HasSuffix(target, ".d") || strings.HasSuffix(target, "/") {
						kind = "configuration folder"
					}
					add(source, kind, false, true)
				}
			}
		}
		contextPath, dockerfile := "", "Dockerfile"
		if build, ok := service["build"].(string); ok {
			contextPath = build
		} else if build, ok := service["build"].(map[string]any); ok {
			contextPath, _ = build["context"].(string)
			if value, ok := build["dockerfile"].(string); ok {
				dockerfile = value
			}
			if build["dockerfile_inline"] != nil {
				continue
			}
		}
		if contextPath != "" {
			resolved, err := resolveTemplatePath(contextPath, env)
			if err != nil {
				result.Notes = append(result.Notes, name+": fill the build context variable first")
				continue
			}
			resolved = path.Clean(resolved)
			if strings.Contains(resolved, "://") || path.IsAbs(resolved) || resolved == ".." || strings.HasPrefix(resolved, "../") {
				result.Notes = append(result.Notes, name+": this build context needs a complete source checkout or an existing host path")
				continue
			}
			directories = append(directories, resolved)
			add(path.Join(resolved, dockerfile), "build", false, true)
		}
	}
	if doc["include"] != nil {
		result.Notes = append(result.Notes, "Compose include requires a complete source project; it is not flattened by file import")
	}
	keys := []string{}
	for key := range requirements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.Requirements = append(result.Requirements, requirements[key])
	}
	return result, directories, nil
}

func (s *TemplateLibrary) TemplateFiles(ctx context.Context, input TemplateFilesInput) (TemplateFilesResult, error) {
	result, directories, err := planTemplateFiles(input)
	if err != nil || !input.Fetch {
		return result, err
	}
	address, err := publicComposeURL(input.Source)
	if err != nil {
		return result, err
	}
	address, err = s.pinFile(ctx, address)
	if err != nil {
		return result, err
	}
	result.Source = address
	parsedSource, _ := url.Parse(address)
	parts := strings.Split(strings.Trim(parsedSource.Path, "/"), "/")
	revision := parts[2]
	base := path.Dir(strings.Join(parts[3:], "/"))
	if base == "." {
		base = ""
	}
	raw, err := s.fetch(ctx, "https://api.github.com/repos/"+parts[0]+"/"+parts[1]+"/git/trees/"+revision+"?recursive=1")
	if err != nil {
		return result, err
	}
	var tree struct {
		Truncated bool
		Tree      []struct {
			Path, Type, Mode string
			Size             int
		}
	}
	if json.Unmarshal(raw, &tree) != nil || tree.Truncated {
		return result, fmt.Errorf("repository file listing is incomplete")
	}
	required := map[string]TemplateFileRequirement{}
	for _, file := range result.Requirements {
		required[path.Join(base, file.Path)] = file
	}
	for _, entry := range tree.Tree {
		if entry.Type == "tree" {
			if requirement, ok := required[entry.Path]; ok {
				directories = append(directories, requirement.Path)
				for i := range result.Requirements {
					if result.Requirements[i].Path == requirement.Path {
						result.Requirements[i].Kind = "configuration folder"
					}
				}
			}
		}
	}
	selected := map[string]struct {
		name, mode string
		secret     bool
	}{}
	total := 0
	for _, entry := range tree.Tree {
		if entry.Type == "commit" {
			for _, directory := range directories {
				prefix := path.Join(base, directory)
				if prefix == "." || entry.Path == prefix || strings.HasPrefix(entry.Path, prefix+"/") {
					return result, fmt.Errorf("source contains a Git submodule; use a complete checkout")
				}
			}
		}
		if entry.Type != "blob" {
			continue
		}
		req, exact := required[entry.Path]
		example := false
		if _, needsEnv := required[path.Join(base, ".env")]; needsEnv && (entry.Path == path.Join(base, ".env.example") || entry.Path == path.Join(base, ".env.sample") || entry.Path == path.Join(base, ".env")) {
			example = true
		}
		wanted := exact || example
		for _, directory := range directories {
			prefix := strings.TrimSuffix(path.Join(base, directory), "/")
			if prefix == "." {
				prefix = ""
			}
			if prefix == "" || strings.HasPrefix(entry.Path, prefix+"/") {
				wanted = true
			}
		}
		if !wanted {
			continue
		}
		relative := entry.Path
		if base != "" {
			if !strings.HasPrefix(entry.Path, base+"/") {
				return result, fmt.Errorf("source assets leave the Compose folder")
			}
			relative = strings.TrimPrefix(entry.Path, base+"/")
		}
		clean, err := templateFilePath(relative)
		if err != nil {
			if relative == ".env" && example {
				clean = ".env"
			} else if exact {
				return result, err
			} else {
				continue
			}
		}
		if entry.Mode == "120000" {
			return result, fmt.Errorf("source contains a symlink (%s); use a complete checkout", relative)
		}
		if entry.Size > 256<<10 {
			return result, fmt.Errorf("source file %s exceeds 256 KiB; use a complete checkout", relative)
		}
		total += entry.Size
		if len(selected) >= 128 || total > 4<<20 {
			return result, fmt.Errorf("source project exceeds automatic import limits (128 files / 4 MiB); use a complete checkout")
		}
		selected[entry.Path] = struct {
			name, mode string
			secret     bool
		}{clean, entry.Mode, req.Secret}
	}
	var lock sync.Mutex
	var jobs sync.WaitGroup
	var firstErr error
	actualBytes := 0
	slots := make(chan struct{}, 4)
	for filename, entry := range selected {
		jobs.Add(1)
		slots <- struct{}{}
		go func(filename string, entry struct {
			name, mode string
			secret     bool
		}) {
			defer jobs.Done()
			defer func() { <-slots }()
			fileURL := (&url.URL{Scheme: "https", Host: "raw.githubusercontent.com", Path: "/" + parts[0] + "/" + parts[1] + "/" + revision + "/" + filename}).String()
			raw, err := s.fetchLimit(ctx, fileURL, 256<<10)
			if err != nil {
				lock.Lock()
				if firstErr == nil {
					firstErr = err
				}
				lock.Unlock()
				return
			}
			lock.Lock()
			defer lock.Unlock()
			actualBytes += len(raw)
			if actualBytes > 4<<20 {
				if firstErr == nil {
					firstErr = fmt.Errorf("source files exceed 4 MiB")
				}
				return
			}
			if entry.name == ".env" || entry.name == ".env.example" || entry.name == ".env.sample" {
				if _, needed := required[path.Join(base, ".env")]; needed && (result.EnvExample == "" || entry.name == ".env") {
					result.EnvExample = string(raw)
					result.EnvExampleSource = fileURL
				}
				if entry.name == ".env" {
					return
				}
			}
			file := TemplateProjectFile{Content: string(raw), Secret: entry.secret, Executable: entry.mode == "100755", Source: fileURL, Sample: entry.secret}
			if !utf8.Valid(raw) {
				file.Content = base64.StdEncoding.EncodeToString(raw)
				file.Encoding = "base64"
			}
			result.Files[entry.name] = file
		}(filename, entry)
	}
	jobs.Wait()
	if firstErr != nil {
		return result, firstErr
	}

	for _, file := range result.Requirements {
		if file.Path == ".env" {
			result.Notes = append(result.Notes, "Set Environment values in app settings; use the fetched example if available.")
			continue
		}
		found := false
		for name := range result.Files {
			if name == file.Path || strings.HasPrefix(name, file.Path+"/") {
				found = true
				break
			}
		}
		if !found {
			result.Notes = append(result.Notes, file.Path+": not present upstream; create it in the file form")
		}
	}
	return result, nil
}

func templateFileMode(file TemplateProjectFile) string {
	if file.Secret {
		return "600"
	}
	if file.Executable {
		return "755"
	}
	return "644"
}
func templateOwner(file TemplateProjectFile) string {
	uid, gid := os.Getuid(), os.Getgid()
	if file.OwnerUID != nil {
		uid = *file.OwnerUID
	}
	if file.OwnerGID != nil {
		gid = *file.OwnerGID
	}
	return strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
}
