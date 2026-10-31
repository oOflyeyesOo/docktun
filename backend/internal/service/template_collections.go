package service

// Catalogs are public data, never shell commands. Conversion only produces an
// editable Compose draft; the existing storage review still gates deployment.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const PortainerCollection = "https://raw.githubusercontent.com/Lissy93/portainer-templates/main/templates.json"

type templateCollection struct {
	items   []CommunityTemplate
	entries map[string]json.RawMessage
	updated time.Time
}

var templateMarkup = regexp.MustCompile(`<[^>]*>|\[(?:/?(?:br|b|i|u|span|p|strong|em))[^\]]*\]`)
var templateName = regexp.MustCompile(`[^a-z0-9_-]+`)
var templateVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func templateText(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(templateMarkup.ReplaceAllString(value, " "))), " ")
}

func templateSlug(value string) string {
	value = strings.Trim(templateName.ReplaceAllString(strings.ToLower(value), "-"), "-_")
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		value = "app-" + value
	}
	return value
}

// Only exact public GitHub hosts are accepted, including for nested references.
func publicTemplateFile(address, extension string) (string, error) {
	if extension == ".yml" {
		return publicComposeURL(address)
	}
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("use a public HTTPS GitHub file URL without credentials or query parameters")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Host == "github.com" && len(parts) >= 5 && parts[2] == "blob" {
		parts = append(parts[:2], parts[3:]...)
		u.Host = "raw.githubusercontent.com"
		u.Path = "/" + strings.Join(parts, "/")
	}
	if u.Host != "raw.githubusercontent.com" || len(parts) < 4 || strings.Contains(u.Path, "..") || !strings.HasSuffix(strings.ToLower(u.Path), extension) {
		return "", fmt.Errorf("select a public GitHub %s file", extension)
	}
	return u.String(), nil
}

func (s *TemplateLibrary) pinFile(ctx context.Context, address string) (string, error) {
	u, _ := url.Parse(address)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 {
		return "", fmt.Errorf("invalid GitHub file path")
	}
	if len(parts[2]) == 40 {
		if _, err := hex.DecodeString(parts[2]); err == nil {
			return address, nil
		}
	}
	var lastErr error
	for end := 3; end < len(parts) && end <= 10; end++ {
		ref := strings.Join(parts[2:end], "/")
		raw, err := s.fetch(ctx, "https://api.github.com/repos/"+parts[0]+"/"+parts[1]+"/commits/"+url.PathEscape(ref))
		if err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "HTTP 404") {
				continue
			}
			return "", err
		}
		var commit struct {
			SHA string `json:"sha"`
		}
		if json.Unmarshal(raw, &commit) != nil || len(commit.SHA) != 40 {
			return "", fmt.Errorf("invalid public repository revision")
		}
		if _, err := hex.DecodeString(commit.SHA); err != nil {
			return "", fmt.Errorf("invalid repository revision")
		}
		pinned := append([]string{parts[0], parts[1], commit.SHA}, parts[end:]...)
		u.Path = "/" + strings.Join(pinned, "/")
		u.RawPath = ""
		return u.String(), nil
	}
	return "", fmt.Errorf("cannot resolve public file revision: %w", lastErr)
}

func (s *TemplateLibrary) PortainerCatalog(ctx context.Context, address string) ([]CommunityTemplate, error) {
	canonical, err := publicTemplateFile(address, ".json")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.collections[canonical]; ok && time.Since(cached.updated) < 10*time.Minute {
		return cached.items, nil
	}
	pinned, err := s.pinFile(ctx, canonical)
	if err != nil {
		return nil, err
	}
	raw, err := s.fetchLimit(ctx, pinned, 8*1024*1024)
	if err != nil {
		return nil, err
	}
	var document struct {
		Templates []json.RawMessage `json:"templates"`
	}
	if json.Unmarshal(raw, &document) != nil || len(document.Templates) == 0 || len(document.Templates) > 5000 {
		return nil, fmt.Errorf("expected a Portainer JSON catalog with 1–5000 templates")
	}
	collection := templateCollection{updated: time.Now(), entries: map[string]json.RawMessage{}}
	for _, entry := range document.Templates {
		var item portainerTemplate
		if err := json.Unmarshal(entry, &item); err != nil {
			return nil, fmt.Errorf("invalid Portainer entry: %w", err)
		}
		checksum := sha256.Sum256(entry)
		id := hex.EncodeToString(checksum[:])
		if _, ok := collection.entries[id]; ok {
			continue
		}
		name := templateText(item.Title)
		if name == "" {
			name = templateText(item.Name)
		}
		if name == "" {
			continue
		}
		description := templateText(item.Description)
		if description == "" {
			description = "Community template for " + name + ". Review upstream setup instructions before deploying."
		}
		unavailable := ""
		if item.Type == 2 {
			unavailable = "Requires Docker Swarm; not a Compose stack"
		} else if item.Type == 1 {
			_, err = portainerCompose(entry)
			if err != nil {
				unavailable = err.Error()
			}
		} else if item.Type != 3 {
			unavailable = "Unsupported Portainer template type"
		}
		if item.Platform != "" && item.Platform != "linux" {
			unavailable = "Requires " + item.Platform
		}
		catalogURL, _ := url.Parse(pinned)
		catalogParts := strings.Split(strings.Trim(catalogURL.Path, "/"), "/")
		collection.items = append(collection.items, CommunityTemplate{ID: id, Name: name, Description: description, Categories: item.Categories, Source: "Portainer · " + catalogParts[0] + "/" + catalogParts[1], Collection: pinned, Documentation: pinned, Unavailable: unavailable})
		collection.entries[id] = entry
	}
	if s.collections == nil {
		s.collections = map[string]templateCollection{}
	}
	// Bound memory even when users try many public collections.
	if len(s.collections) >= 12 {
		s.collections = map[string]templateCollection{}
	}
	s.collections[canonical] = collection
	s.collections[pinned] = collection
	return collection.items, nil
}

type portainerTemplate struct {
	Type        int      `json:"type"`
	Title       string   `json:"title"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Note        string   `json:"note"`
	Platform    string   `json:"platform"`
	Categories  []string `json:"categories"`
	Image       string   `json:"image"`
	Network     string   `json:"network"`
	Restart     string   `json:"restart_policy"`
	Hostname    string   `json:"hostname"`
	Command     any      `json:"command"`
	Privileged  bool     `json:"privileged"`
	Interactive bool     `json:"interactive"`
	Ports       []string `json:"ports"`
	Volumes     []struct {
		Container string `json:"container"`
		Bind      string `json:"bind"`
		Readonly  bool   `json:"readonly"`
	} `json:"volumes"`
	Env []struct {
		Name        string  `json:"name"`
		Default     *string `json:"default"`
		Label       string  `json:"label"`
		Description string  `json:"description"`
		Select      []struct {
			Value   string `json:"value"`
			Default bool   `json:"default"`
		} `json:"select"`
	} `json:"env"`
	Labels []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"labels"`
	Repository struct {
		URL       string `json:"url"`
		Stackfile string `json:"stackfile"`
	} `json:"repository"`
}

func literalTemplate(value string) string { return strings.ReplaceAll(value, "$", "$$") }

func portainerCompose(raw []byte) (string, error) {
	var item portainerTemplate
	if err := json.Unmarshal(raw, &item); err != nil {
		return "", err
	}
	if item.Type != 1 || item.Image == "" {
		return "", fmt.Errorf("container template has no image")
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	allowed := " type title name description platform categories image network restart_policy hostname command privileged interactive ports volumes env labels repository logo note maintainer id "
	for field := range fields {
		if !strings.Contains(allowed, " "+field+" ") {
			return "", fmt.Errorf("requires unsupported Portainer setting: %s", field)
		}
	}
	for field, supported := range map[string]string{"volumes": " container bind readonly ", "env": " name default label description select preset ", "labels": " name value "} {
		entries, _ := fields[field].([]any)
		for _, entry := range entries {
			object, _ := entry.(map[string]any)
			for key := range object {
				if !strings.Contains(supported, " "+key+" ") {
					return "", fmt.Errorf("requires unsupported Portainer setting: %s.%s", field, key)
				}
			}
		}
	}
	service := map[string]any{"image": literalTemplate(item.Image)}
	service["restart"] = "always"
	if item.Restart != "" {
		service["restart"] = item.Restart
	}
	if item.Hostname != "" {
		service["hostname"] = literalTemplate(item.Hostname)
	}
	if item.Privileged {
		service["privileged"] = true
	}
	if item.Interactive {
		service["stdin_open"] = true
		service["tty"] = true
	}
	if item.Command != nil {
		switch command := item.Command.(type) {
		case string:
			service["command"] = literalTemplate(command)
		case []any:
			parts := []string{}
			for _, value := range command {
				text, ok := value.(string)
				if !ok {
					return "", fmt.Errorf("invalid container command")
				}
				parts = append(parts, literalTemplate(text))
			}
			service["command"] = parts
		default:
			return "", fmt.Errorf("invalid container command")
		}
	}
	if len(item.Ports) > 0 && item.Network != "host" && item.Network != "none" {
		ports := []string{}
		for _, port := range item.Ports {
			ports = append(ports, literalTemplate(port))
		}
		service["ports"] = ports
	}
	document := map[string]any{}
	if item.Network == "host" || item.Network == "none" {
		service["network_mode"] = item.Network
	} else if item.Network != "" && item.Network != "bridge" {
		service["networks"] = []string{"template-network"}
		document["networks"] = map[string]any{"template-network": map[string]any{"external": true, "name": literalTemplate(item.Network)}}
	}
	env := map[string]string{}
	notes := []string{"# " + templateText(item.Description), "# Setup: " + templateText(item.Note)}
	for _, variable := range item.Env {
		if !templateVariable.MatchString(variable.Name) {
			return "", fmt.Errorf("invalid environment variable: %s", variable.Name)
		}
		value := variable.Default
		for _, option := range variable.Select {
			if option.Default {
				selected := option.Value
				value = &selected
			}
		}
		if value == nil {
			env[variable.Name] = "${" + variable.Name + "?Set " + variable.Name + " in Environment before deploying}"
		} else {
			env[variable.Name] = literalTemplate(*value)
		}
		notes = append(notes, "# "+variable.Name+": "+templateText(variable.Label+" "+variable.Description))
	}
	if len(env) > 0 {
		service["environment"] = env
	}
	volumes := []any{}
	named := map[string]any{}
	for index, volume := range item.Volumes {
		if !strings.HasPrefix(volume.Container, "/") {
			return "", fmt.Errorf("invalid container volume path")
		}
		target := volume.Container
		readOnly := volume.Readonly
		if strings.HasSuffix(target, ":ro") {
			target, readOnly = strings.TrimSuffix(target, ":ro"), true
		} else if strings.HasSuffix(target, ":rw") {
			target = strings.TrimSuffix(target, ":rw")
		}
		if strings.Contains(target, ":") {
			return "", fmt.Errorf("unsupported inline volume options in %s; use a Compose template with explicit mount settings", volume.Container)
		}
		// This conversion creates a new editable draft, never migrates an existing
		// database. The official image changed its default data layout in v18.
		if modernPostgresTemplate(item.Image) && target == "/var/lib/postgresql/data" && env["PGDATA"] == "" {
			target = "/var/lib/postgresql"
			notes = append(notes, "# Storage: PostgreSQL 18+ uses /var/lib/postgresql. Existing databases require a separate major-version migration.")
		}
		mount := map[string]any{"target": literalTemplate(target), "read_only": readOnly}
		if volume.Bind != "" {
			mount["type"] = "bind"
			source := volume.Bind
			if trimmed := strings.TrimSpace(source); strings.HasPrefix(trimmed, "/") && trimmed != source {
				source = trimmed
				notes = append(notes, "# Storage: removed surrounding whitespace from the imported host path for "+target+". Review the mount source before deployment.")
			}
			mount["source"] = literalTemplate(source)
			mount["bind"] = map[string]any{"create_host_path": false}
		} else {
			name := fmt.Sprintf("data-%d", index+1)
			mount["type"] = "volume"
			mount["source"] = name
			named[name] = map[string]any{}
		}
		volumes = append(volumes, mount)
	}
	if len(volumes) > 0 {
		service["volumes"] = volumes
	}
	if len(named) > 0 {
		document["volumes"] = named
	}
	labels := map[string]string{}
	for _, label := range item.Labels {
		labels[label.Name] = literalTemplate(label.Value)
	}
	if len(labels) > 0 {
		service["labels"] = labels
	}
	document["services"] = map[string]any{templateSlug(item.Title): service}
	content, err := yaml.Marshal(document)
	return strings.Join(notes, "\n") + "\n" + string(content), err
}

func modernPostgresTemplate(image string) bool {
	if strings.Contains(image, "@") {
		return false // A digest alone does not reveal its major version.
	}
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "library/")
	repository, tag, tagged := strings.Cut(image, ":")
	if repository != "postgres" {
		return false
	}
	if !tagged || tag == "latest" {
		return true
	}
	major := strings.FieldsFunc(tag, func(r rune) bool { return r == '.' || r == '-' })
	if len(major) == 0 {
		return false
	}
	version, err := strconv.Atoi(major[0])
	return err == nil && version >= 18
}

func (s *TemplateLibrary) ImportPortainer(ctx context.Context, collectionURL, id string) (string, string, error) {
	items, err := s.PortainerCatalog(ctx, collectionURL)
	if err != nil {
		return "", "", err
	}
	var selected *CommunityTemplate
	for i := range items {
		if items[i].ID == id {
			selected = &items[i]
			break
		}
	}
	if selected == nil {
		return "", "", fmt.Errorf("template changed or was removed; refresh the catalog")
	}
	if selected.Unavailable != "" {
		return "", "", fmt.Errorf("%s", selected.Unavailable)
	}
	s.mu.Lock()
	raw := s.collections[selected.Collection].entries[id]
	s.mu.Unlock()
	var item portainerTemplate
	if err = json.Unmarshal(raw, &item); err != nil {
		return "", "", err
	}
	source := selected.Collection + "#" + id
	if item.Type == 1 {
		content, err := portainerCompose(raw)
		return content, source, err
	}
	u, err := url.Parse(item.Repository.URL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(strings.Split(strings.Trim(u.Path, "/"), "/")) != 2 {
		return "", "", fmt.Errorf("stack repository must be a public GitHub repository")
	}
	file := strings.TrimPrefix(item.Repository.Stackfile, "/")
	if file == "" || strings.Contains(file, "..") {
		return "", "", fmt.Errorf("invalid stack file path")
	}
	address, err := publicComposeURL("https://raw.githubusercontent.com" + strings.TrimSuffix(u.Path, "/") + "/HEAD/" + file)
	if err != nil {
		return "", "", err
	}
	address, err = s.pinFile(ctx, address)
	if err != nil {
		return "", "", err
	}
	content, _, err := s.Import(ctx, address)
	if err != nil {
		return "", "", err
	}
	// Defaults and instructions stay visible without inserting secrets into the
	// dashboard environment. Missing stack variables are caught by Compose review.
	notes := []string{"# Source: " + address, "# Setup: " + templateText(item.Note)}
	for _, v := range item.Env {
		if v.Default != nil {
			notes = append(notes, "# Default "+templateText(v.Name+"="+*v.Default))
		}
		notes = append(notes, "# "+templateText(v.Name+" "+v.Label+" "+v.Description))
	}
	return strings.Join(notes, "\n") + "\n" + content, source, nil
}
