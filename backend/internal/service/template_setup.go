package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Setup edits a draft only. Actual resource creation remains in the audited
// project deployment flow, after Compose and Docker-host storage validation.
type TemplateSetupInput struct {
	Compose string                        `json:"compose_content"`
	Env     string                        `json:"env_content"`
	Apply   bool                          `json:"apply"`
	Values  map[string]string             `json:"values"`
	Storage map[string]SetupStorageChoice `json:"storage"`
}
type SetupStorageChoice struct {
	Mode   string `json:"mode"`
	Source string `json:"source"`
}
type SetupField struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Service  string `json:"service"`
	Value    string `json:"value"`
	Kind     string `json:"kind"`
	Hint     string `json:"hint"`
	Required bool   `json:"required"`
	Password bool   `json:"password"`
	Secret   bool   `json:"secret"`
}
type SetupStorage struct {
	ID        string `json:"id"`
	Service   string `json:"service"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	ReadOnly  bool   `json:"read_only"`
	CanManage bool   `json:"can_manage"`
	Hint      string `json:"hint"`
}
type TemplateSetup struct {
	Fields  []SetupField   `json:"fields"`
	Storage []SetupStorage `json:"storage"`
	Notes   []string       `json:"notes"`
	Compose string         `json:"compose_content,omitempty"`
	Env     string         `json:"env_content"`
}

var setupInterpolation = regexp.MustCompile(`\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:-|-|:\?|\?|:\+|\+)([^{}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
var setupPassword = regexp.MustCompile(`(?i)(password|passwd|(^|_)pass($|_))`)
var setupSecret = regexp.MustCompile(`(?i)(password|passwd|secret|token|api_?key|auth_?key|(^|_)pass($|_))`)
var setupSimplePort = regexp.MustCompile(`^((?:[0-9.]+:)?)([0-9]+):([0-9]+)(/(?:tcp|udp))?$`)

func setupGet(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
func setupString(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	return node.Value
}
func setupSet(node *yaml.Node, value string) {
	node.Kind = yaml.ScalarNode
	node.Tag = "!!str"
	node.Value = value
	node.Style = 0
	node.Content = nil
}
func setupPut(node *yaml.Node, key string, value *yaml.Node) {
	if current := setupGet(node, key); current != nil {
		*current = *value
		return
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

// A conservative dotenv reader: do not silently reinterpret advanced existing
// interpolation/multiline expressions. Those remain usable in the raw editor.
func setupEnv(raw string) (map[string]string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || !templateVariable.MatchString(key) {
			return nil, fmt.Errorf("Environment contains an advanced or invalid line; use the advanced editor for this draft")
		}
		if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return nil, fmt.Errorf("multiline Environment values need the advanced editor")
			}
			value = strings.ReplaceAll(value[1:len(value)-1], `\'`, "'")
		} else {
			if strings.HasPrefix(value, `"`) {
				// Compose accepts backslash-dollar in double quotes. Decode that
				// literal without treating genuine .env interpolation as a value.
				var quoted strings.Builder
				for i := 0; i < len(value); i++ {
					if value[i] == '\\' && i+1 < len(value) {
						if value[i+1] == '$' {
							quoted.WriteByte('$')
						} else {
							quoted.WriteByte('\\')
							quoted.WriteByte(value[i+1])
						}
						i++
						continue
					}
					if value[i] == '$' {
						return nil, fmt.Errorf("Environment interpolation needs the advanced editor; literal values can be single-quoted")
					}
					quoted.WriteByte(value[i])
				}
				var decoded string
				if json.Unmarshal([]byte(quoted.String()), &decoded) != nil {
					return nil, fmt.Errorf("this quoted Environment value needs the advanced editor")
				}
				value = decoded
			} else {
				if strings.Contains(value, "$") {
					return nil, fmt.Errorf("Environment interpolation needs the advanced editor; literal values can be single-quoted")
				}
				if index := strings.Index(value, " #"); index >= 0 {
					value = strings.TrimSpace(value[:index])
				}
			}
		}
		values[key] = value
	}
	return values, nil
}
func setupQuote(value string) string {
	// A final backslash would escape a single quote's closing delimiter.
	if strings.HasSuffix(value, `\`) {
		return strings.ReplaceAll(strconv.Quote(value), "$", `\$`)
	}
	return "'" + strings.ReplaceAll(value, "'", `\'`) + "'"
}

func setupMountParts(value string) []string {
	parts := []string{}
	start, depth := 0, 0
	for i := 0; i < len(value); i++ {
		if value[i] == '{' && i > 0 && value[i-1] == '$' {
			depth++
		}
		if value[i] == '}' && depth > 0 {
			depth--
		}
		if value[i] == ':' && depth == 0 {
			parts = append(parts, value[start:i])
			start = i + 1
		}
	}
	return append(parts, value[start:])
}

func ConfigureTemplate(input TemplateSetupInput) (TemplateSetup, error) {
	result := TemplateSetup{Fields: []SetupField{}, Storage: []SetupStorage{}, Notes: []string{}}
	if len(input.Compose) > 1<<20 || len(input.Env) > 128<<10 || strings.TrimSpace(input.Compose) == "" {
		return result, fmt.Errorf("provide a Compose draft up to 1 MiB and Environment up to 128 KiB")
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(input.Compose), &document); err != nil || len(document.Content) != 1 {
		return result, fmt.Errorf("invalid Compose YAML")
	}
	root := document.Content[0]
	services := setupGet(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return result, fmt.Errorf("Compose must contain a services mapping")
	}
	env, err := setupEnv(input.Env)
	if err != nil {
		return result, err
	}
	setters := map[string]func(string) error{}
	mountSetters := map[string]func(SetupStorageChoice) error{}
	createdVolumes := map[string]bool{}
	vars := map[string]int{}
	counter := 0
	addField := func(id, name, service, value, kind, hint string, required bool, set func(string) error) {
		if name == "PUID" || name == "PGID" {
			hint = "User/group ID that owns the data on your Docker host. NAS IDs may differ from 1000."
		}
		if name == "TZ" {
			hint = "Timezone, for example America/Chicago."
		}
		secret := setupSecret.MatchString(name)
		password := setupPassword.MatchString(name) && !strings.HasSuffix(strings.ToUpper(name), "_FILE") && !strings.Contains(strings.ToUpper(name), "HASH")
		result.Fields = append(result.Fields, SetupField{ID: id, Name: name, Service: service, Value: value, Kind: kind, Hint: hint, Required: required, Password: password, Secret: secret})
		setters[id] = set
	}
	var walk func(*yaml.Node, int) error
	walk = func(node *yaml.Node, depth int) error {
		if depth > 64 {
			return fmt.Errorf("Compose nesting is too deep")
		}
		if node.Kind == yaml.AliasNode {
			result.Notes = append(result.Notes, "YAML aliases are preserved. Settings inherited through an alias may need the advanced editor.")
			return nil
		}
		if node.Kind == yaml.ScalarNode {
			for _, match := range setupInterpolation.FindAllStringSubmatch(node.Value, -1) {
				if match[0] == "$$" {
					continue
				}
				name := match[1]
				if name == "" {
					name = match[4]
				}
				if _, exists := vars[name]; exists {
					continue
				}
				value, provided := env[name]
				if (!provided && (match[2] == ":-" || match[2] == "-")) || (provided && value == "" && match[2] == ":-") {
					value = match[3]
				}
				hint := "Shared Environment value; updates every reference to " + name + "."
				if name == "TZ" {
					hint = "Timezone, for example America/Chicago."
				}
				if name == "PUID" || name == "PGID" {
					hint = "User/group ID that owns the data on your Docker host. NAS IDs may differ from 1000."
				}
				vars[name] = len(result.Fields)
				addField("var:"+name, name, "Shared", value, "environment", hint, (match[2] == ":?" && value == "") || (!provided && (match[2] == "" || match[2] == "?" || match[2] == ":?")), func(value string) error { env[name] = value; return nil })
			}
		}
		for i, child := range node.Content {
			if node.Kind == yaml.MappingNode && i%2 == 0 {
				continue
			}
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err = walk(root, 0); err != nil {
		return result, err
	}
	// Explicit .env entries are also editable even if a template consumes them
	// through env_file rather than interpolation. Never expose dashboard env.
	keys := []string{}
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, exists := vars[key]; !exists {
			name := key
			addField("var:"+name, name, "Environment", env[name], "environment", "Stack Environment value.", false, func(value string) error { env[name] = value; return nil })
		}
	}
	addLiteral := func(node *yaml.Node, name, service, kind, hint string, set func(string) error) {
		if node == nil || node.Kind != yaml.ScalarNode || node.Tag == "!!null" || strings.Contains(node.Value, "$") {
			return
		}
		counter++
		addField(fmt.Sprintf("field:%d", counter), name, service, node.Value, kind, hint, false, set)
	}
	for i := 0; i+1 < len(services.Content); i += 2 {
		name, service := services.Content[i].Value, services.Content[i+1]
		if service.Kind != yaml.MappingNode {
			continue
		}
		if environment := setupGet(service, "environment"); environment != nil {
			if environment.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(environment.Content); j += 2 {
					key, node := environment.Content[j].Value, environment.Content[j+1]
					addLiteral(node, key, name, "environment", "Container setting. Use upstream instructions for app-specific requirements.", func(value string) error { setupSet(node, literalTemplate(value)); return nil })
				}
			} else if environment.Kind == yaml.SequenceNode {
				for _, node := range environment.Content {
					key, value, ok := strings.Cut(node.Value, "=")
					if !ok || strings.Contains(value, "$") {
						continue
					}
					copy := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
					addLiteral(copy, key, name, "environment", "Container setting.", func(value string) error { setupSet(node, key+"="+literalTemplate(value)); return nil })
				}
			}
		}
		if node := setupGet(service, "container_name"); node != nil {
			addLiteral(node, "Container name", name, "container", "Must be unique on the Docker host.", func(value string) error { setupSet(node, literalTemplate(value)); return nil })
		}
		if ports := setupGet(service, "ports"); ports != nil && ports.Kind == yaml.SequenceNode {
			for _, node := range ports.Content {
				if node.Kind == yaml.ScalarNode {
					match := setupSimplePort.FindStringSubmatch(node.Value)
					if match == nil {
						continue
					}
					copy := &yaml.Node{Kind: yaml.ScalarNode, Value: match[2]}
					addLiteral(copy, "Host port → "+match[3]+match[4], name, "port", "Choose an unused host port (1–65535).", func(value string) error {
						port, err := strconv.Atoi(value)
						if err != nil || port < 1 || port > 65535 {
							return fmt.Errorf("host port must be 1–65535")
						}
						setupSet(node, match[1]+value+":"+match[3]+match[4])
						return nil
					})
				} else if published := setupGet(node, "published"); published != nil {
					addLiteral(published, "Host port", name, "port", "Choose an unused host port (1–65535).", func(value string) error {
						port, err := strconv.Atoi(value)
						if err != nil || port < 1 || port > 65535 {
							return fmt.Errorf("host port must be 1–65535")
						}
						setupSet(published, value)
						return nil
					})
				}
			}
		}
		if mounts := setupGet(service, "volumes"); mounts != nil && mounts.Kind == yaml.SequenceNode {
			for index, node := range mounts.Content {
				source, target, mode := "", "", ""
				kind := "bind"
				if node.Kind == yaml.ScalarNode {
					parts := setupMountParts(node.Value)
					if len(parts) < 2 || len(parts) > 3 {
						continue
					}
					source, target = parts[0], parts[1]
					if len(parts) == 3 {
						mode = parts[2]
					}
					if !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") && !strings.HasPrefix(source, "~") && !strings.Contains(source, "$") {
						continue
					}
				} else {
					kind = setupString(setupGet(node, "type"))
					source = setupString(setupGet(node, "source"))
					target = setupString(setupGet(node, "target"))
					if setupString(setupGet(node, "read_only")) == "true" {
						mode = "ro"
					}
				}
				if kind != "bind" {
					continue
				}
				readOnly := false
				for _, part := range strings.Split(mode, ",") {
					if part == "ro" {
						readOnly = true
					}
				}
				canManage := strings.HasPrefix(target, "/") && target != "/" && source != "/" && !strings.ContainsAny(target, "$\x00\r\n") && !strings.HasPrefix(target, "/dev") && !strings.HasSuffix(target, ".sock") && !strings.Contains(target[strings.LastIndex(target, "/")+1:], ".")
				for _, special := range []string{"/etc/localtime", "/etc/timezone", "/etc/hostname", "/etc/hosts", "/etc/machine-id"} {
					if source == special || target == special {
						canManage = false
					}
				}
				for _, system := range []string{"/proc", "/sys", "/run", "/var/run"} {
					if target == system || strings.HasPrefix(target, system+"/") {
						canManage = false
					}
				}
				if mode != "" && mode != "ro" && mode != "rw" {
					canManage = false
				}
				if node.Kind == yaml.MappingNode {
					for j := 0; j+1 < len(node.Content); j += 2 {
						if !strings.Contains(" type source target read_only bind ", " "+node.Content[j].Value+" ") {
							canManage = false
						}
					}
					if bind := setupGet(node, "bind"); bind != nil {
						for j := 0; j+1 < len(bind.Content); j += 2 {
							if bind.Content[j].Value != "create_host_path" {
								canManage = false
							}
						}
					}
				}
				id := fmt.Sprintf("mount:%s:%d", name, index)
				hint := "Choose an existing Docker-host folder, or let Docker create a new data volume. Existing files are not copied."
				if !canManage {
					hint = "File, socket, device or advanced mount: select its existing host source; it cannot become an empty data volume."
				}
				result.Storage = append(result.Storage, SetupStorage{ID: id, Service: name, Source: source, Target: target, ReadOnly: readOnly, CanManage: canManage, Hint: hint})
				mountSetters[id] = func(choice SetupStorageChoice) error {
					if choice.Mode == "host" {
						if choice.Source == source {
							return nil
						}
						if choice.Source == "" || strings.ContainsAny(choice.Source, "\x00\r\n") {
							return fmt.Errorf("choose a host source")
						}
						if node.Kind == yaml.ScalarNode {
							suffix := ""
							if mode != "" {
								suffix = ":" + mode
							}
							setupSet(node, literalTemplate(choice.Source)+":"+target+suffix)
						} else {
							setupPut(node, "source", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: literalTemplate(choice.Source)})
						}
						return nil
					}
					if choice.Mode != "managed" || !canManage {
						return fmt.Errorf("this mount needs an existing host source")
					}
					// Shared source paths retain the same volume across services.
					hash := sha256.Sum256([]byte(source))
					volume := "app-data-" + hex.EncodeToString(hash[:6])
					volumes := setupGet(root, "volumes")
					if volumes == nil {
						volumes = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
						setupPut(root, "volumes", volumes)
					}
					if volumes.Kind != yaml.MappingNode {
						return fmt.Errorf("advanced volumes definition needs the YAML editor")
					}
					if setupGet(volumes, volume) != nil && !createdVolumes[volume] {
						return fmt.Errorf("generated volume name conflicts with an existing definition")
					}
					if !createdVolumes[volume] {
						setupPut(volumes, volume, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
						createdVolumes[volume] = true
					}
					mount := map[string]any{"type": "volume", "source": volume, "target": target, "read_only": readOnly}
					var replacement yaml.Node
					replacement.Encode(mount)
					*node = replacement
					return nil
				}
			}
		}
	}
	for _, line := range strings.Split(input.Compose, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			result.Notes = append(result.Notes, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#")))
		}
	}
	result.Notes = append(result.Notes, "Some apps create their first administrator in the app's own web UI. Credentials not declared by the template cannot be configured here.")
	if !input.Apply {
		return result, nil
	}
	if len(input.Values) > 1000 || len(input.Storage) > 500 {
		return result, fmt.Errorf("too many template changes")
	}
	for id, value := range input.Values {
		setter, ok := setters[id]
		if !ok {
			return result, fmt.Errorf("template changed; refresh its setup form")
		}
		if len(value) > 16384 || strings.ContainsAny(value, "\x00\r\n") {
			return result, fmt.Errorf("setup values must be single-line and at most 16 KiB")
		}
		if err = setter(value); err != nil {
			return result, err
		}
	}
	// Parent/child host sources are not equivalent to independent empty volumes.
	for id, choice := range input.Storage {
		if choice.Mode == "managed" {
			for _, a := range result.Storage {
				if a.ID != id {
					continue
				}
				for _, b := range result.Storage {
					if a.Source != b.Source && (strings.HasPrefix(a.Source, strings.TrimRight(b.Source, "/")+"/") || strings.HasPrefix(b.Source, strings.TrimRight(a.Source, "/")+"/")) {
						return result, fmt.Errorf("related host folders need existing host paths to preserve their shared directory layout")
					}
				}
			}
		}
	}
	for _, mount := range result.Storage {
		if input.Storage[mount.ID].Mode != "managed" {
			continue
		}
		for _, other := range result.Storage {
			if other.Source == mount.Source && input.Storage[other.ID].Mode != "managed" {
				return result, fmt.Errorf("shared host sources must use the same storage choice")
			}
		}
	}
	for id, choice := range input.Storage {
		setter, ok := mountSetters[id]
		if !ok {
			return result, fmt.Errorf("template changed; refresh its storage form")
		}
		if err = setter(choice); err != nil {
			return result, err
		}
	}
	raw, err := yaml.Marshal(&document)
	if err != nil {
		return result, err
	}
	result.Compose = string(raw)
	// Preserve existing comments/expressions for unchanged values; only replace
	// explicitly edited Environment entries and append new shared variables.
	lines := strings.Split(input.Env, "\n")
	written := map[string]bool{}
	for i, line := range lines {
		key, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
		key = strings.TrimSpace(key)
		if !ok {
			continue
		}
		if _, changed := input.Values["var:"+key]; changed {
			lines[i] = key + "=" + setupQuote(env[key])
			written[key] = true
		}
	}
	added := []string{}
	for id := range input.Values {
		if strings.HasPrefix(id, "var:") {
			key := strings.TrimPrefix(id, "var:")
			if !written[key] {
				added = append(added, key)
			}
		}
	}
	sort.Strings(added)
	for _, key := range added {
		lines = append(lines, key+"="+setupQuote(env[key]))
	}
	result.Env = strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
	if len(result.Compose) > 1<<20 || len(result.Env) > 128<<10 {
		return result, fmt.Errorf("configured draft exceeds the Compose/Environment limits")
	}
	return result, nil
}
