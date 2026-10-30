package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type SetupDependency struct {
	Service   string `json:"service"`
	Target    string `json:"target"`
	Role      string `json:"role"`
	Suggested bool   `json:"suggested"`
	Healthy   bool   `json:"healthy"`
}
type SetupCredentialLink struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Fields    []string `json:"fields"`
	Uses      []string `json:"uses"`
	Suggested bool     `json:"suggested"`
}
type setupURI struct {
	field   string
	service string
	target  string
	address *url.URL
}

func setupLiteral(value string) (string, bool) {
	for _, match := range setupInterpolation.FindAllString(value, -1) {
		if match != "$$" {
			return "", false
		}
	}
	return strings.ReplaceAll(value, "$$", "$"), true
}

func setupRole(image string) string {
	image = strings.ToLower(image)
	for _, name := range []string{"postgres", "mysql", "mariadb", "mongo"} {
		if strings.Contains(image, name) {
			return "database"
		}
	}
	for _, name := range []string{"redis", "valkey", "memcached"} {
		if strings.Contains(image, name) {
			return "cache"
		}
	}
	return "app"
}

func setupCredentialKind(field SetupField) string {
	name := strings.ToUpper(field.Name)
	if field.Password {
		return "password"
	}
	if strings.HasSuffix(name, "_USER") || strings.HasSuffix(name, "_USERNAME") || name == "USER" || name == "USERNAME" {
		return "username"
	}
	if strings.HasSuffix(name, "_DATABASE") || strings.HasSuffix(name, "_DB") || strings.HasSuffix(name, "_DB_NAME") || name == "DATABASE_NAME" {
		return "database"
	}
	return ""
}

func setupDependencyPlan(root *yaml.Node, result *TemplateSetup, input *TemplateSetupInput) error {
	services := setupGet(root, "services")
	nodes := map[string]*yaml.Node{}
	hostnames := map[string]string{}
	roles := map[string]string{}
	edges := map[string]map[string]bool{}
	for i := 0; i+1 < len(services.Content); i += 2 {
		name, node := services.Content[i].Value, services.Content[i+1]
		nodes[name] = node
		hostnames[name] = name
		roles[name] = setupRole(setupString(setupGet(node, "image")))
		if alias := setupString(setupGet(node, "container_name")); alias != "" {
			hostnames[alias] = name
		}
		edges[name] = map[string]bool{}
	}
	fields := map[string]SetupField{}
	byService := map[string]map[string]string{}
	for _, field := range result.Fields {
		fields[field.ID] = field
		if byService[field.Service] == nil {
			byService[field.Service] = map[string]string{}
		}
		byService[field.Service][field.Name] = field.ID
	}
	uris := []setupURI{}
	proposed := map[string]map[string]bool{}
	for service, node := range nodes {
		dependencies := setupGet(node, "depends_on")
		if dependencies != nil {
			if dependencies.Kind == yaml.SequenceNode {
				for _, target := range dependencies.Content {
					edges[service][target.Value] = true
				}
			} else if dependencies.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(dependencies.Content); i += 2 {
					edges[service][dependencies.Content[i].Value] = true
				}
			}
		}
		proposed[service] = map[string]bool{}
		environment := setupGet(node, "environment")
		if environment == nil {
			continue
		}
		type entry struct{ name, value string }
		entries := []entry{}
		if environment.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(environment.Content); i += 2 {
				entries = append(entries, entry{environment.Content[i].Value, environment.Content[i+1].Value})
			}
		} else if environment.Kind == yaml.SequenceNode {
			for _, item := range environment.Content {
				key, value, ok := strings.Cut(item.Value, "=")
				if ok {
					entries = append(entries, entry{key, value})
				}
			}
		}
		for _, entry := range entries {
			literal, plain := setupLiteral(entry.value)
			host := ""
			parsed, err := url.Parse(literal)
			if plain && err == nil && parsed.Hostname() != "" {
				host = parsed.Hostname()
				if id := byService[service][entry.name]; id != "" {
					if target := hostnames[host]; target != "" {
						uris = append(uris, setupURI{id, service, target, parsed})
					}
				}
			} else if strings.HasSuffix(strings.ToUpper(entry.name), "_HOST") || strings.HasSuffix(strings.ToUpper(entry.name), "_HOSTNAME") {
				if plain {
					host = strings.Split(literal, ":")[0]
				}
			} else if at := strings.LastIndex(entry.value, "@"); at >= 0 {
				host = strings.Split(strings.Split(entry.value[at+1:], "/")[0], ":")[0]
			}
			target := hostnames[host]
			if target != "" && target != service && roles[target] != "app" {
				proposed[service][target] = true
			}
		}
	}
	names := []string{}
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, service := range names {
		targets := map[string]bool{}
		for target := range edges[service] {
			targets[target] = true
		}
		for target := range proposed[service] {
			targets[target] = true
		}
		ordered := []string{}
		for target := range targets {
			ordered = append(ordered, target)
		}
		sort.Strings(ordered)
		for _, target := range ordered {
			health := setupGet(nodes[target], "healthcheck")
			healthy := health != nil && setupString(setupGet(health, "disable")) != "true"
			if edges[service][target] {
				healthy = setupString(setupGet(setupGet(setupGet(nodes[service], "depends_on"), target), "condition")) == "service_healthy"
			}
			result.Dependencies = append(result.Dependencies, SetupDependency{service, target, roles[target], !edges[service][target], healthy})
		}
	}
	buckets := map[string][]SetupField{}
	for _, field := range result.Fields {
		kind := setupCredentialKind(field)
		if kind != "" && field.Value != "" {
			buckets[kind+"\x00"+field.Value] = append(buckets[kind+"\x00"+field.Value], field)
		}
	}
	type uriLink struct {
		uri  setupURI
		kind string
		old  string
	}
	uriBindings := map[string][]uriLink{}
	keys := []string{}
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		members := buckets[key]
		kind, value, _ := strings.Cut(key, "\x00")
		ids, uses := []string{}, []string{}
		suggested := false
		for _, field := range members {
			ids = append(ids, field.ID)
			uses = append(uses, field.Service+" · "+field.Name)
		}
		bindings := []uriLink{}
		for _, uri := range uris {
			matches := false
			if kind == "password" && uri.address.User != nil {
				password, ok := uri.address.User.Password()
				matches = ok && password == value
			}
			if kind == "username" && uri.address.User != nil {
				matches = uri.address.User.Username() == value
			}
			if kind == "database" {
				matches = strings.TrimPrefix(uri.address.Path, "/") == value
			}
			if !matches {
				continue
			}
			for _, field := range members {
				if field.Service == uri.target {
					bindings = append(bindings, uriLink{uri, kind, value})
					uses = append(uses, uri.service+" · connection URL")
					suggested = true
					break
				}
			}
		}
		if len(ids) < 2 && len(bindings) == 0 {
			continue
		}
		for _, a := range members {
			for _, b := range members {
				if a.Service != b.Service && (edges[a.Service][b.Service] || edges[b.Service][a.Service] || proposed[a.Service][b.Service] || proposed[b.Service][a.Service]) {
					suggested = true
				}
			}
		}
		sort.Strings(ids)
		digest := sha256.Sum256([]byte(kind + "\x00" + strings.Join(ids, "\x00")))
		id := hex.EncodeToString(digest[:8])
		result.CredentialLinks = append(result.CredentialLinks, SetupCredentialLink{id, kind, ids, uses, suggested})
		uriBindings[id] = bindings
	}
	if !input.Apply {
		return nil
	}
	edits := map[string]string{}
	for id, value := range input.Values {
		edits[id] = value
	}
	input.Values = edits
	for id, value := range input.CredentialLinks {
		var link *SetupCredentialLink
		for i := range result.CredentialLinks {
			if result.CredentialLinks[i].ID == id {
				link = &result.CredentialLinks[i]
				break
			}
		}
		if link == nil {
			return fmt.Errorf("credential links changed; reload app setup")
		}
		for _, field := range link.Fields {
			if existing, ok := input.Values[field]; ok && existing != value {
				return fmt.Errorf("linked credentials must have matching values")
			}
			input.Values[field] = value
		}
		for _, binding := range uriBindings[id] {
			raw := fields[binding.uri.field].Value
			if edited, ok := input.Values[binding.uri.field]; ok {
				raw = edited
			}
			address, err := url.Parse(raw)
			if err != nil || address.Hostname() != binding.uri.address.Hostname() {
				return fmt.Errorf("connection URL changed; review its credential links")
			}
			switch binding.kind {
			case "password":
				if address.User == nil {
					return fmt.Errorf("connection URL has no username")
				}
				address.User = url.UserPassword(address.User.Username(), value)
			case "username":
				if address.User == nil {
					return fmt.Errorf("connection URL has no username")
				}
				password, hasPassword := address.User.Password()
				if hasPassword {
					address.User = url.UserPassword(value, password)
				} else {
					address.User = url.User(value)
				}
			case "database":
				address.Path = "/" + value
				address.RawPath = "/" + url.PathEscape(value)
			}
			input.Values[binding.uri.field] = address.String()
		}
	}
	if input.ConnectDependencies {
		for service, targets := range proposed {
			for target := range targets {
				edges[service][target] = true
			}
		}
		visiting, done := map[string]bool{}, map[string]bool{}
		var visit func(string) bool
		visit = func(name string) bool {
			if visiting[name] {
				return false
			}
			if done[name] {
				return true
			}
			visiting[name] = true
			for target := range edges[name] {
				if nodes[target] == nil || !visit(target) {
					return false
				}
			}
			visiting[name] = false
			done[name] = true
			return true
		}
		for name := range nodes {
			if !visit(name) {
				return fmt.Errorf("dependency graph contains a cycle or missing service")
			}
		}
		for service, targets := range proposed {
			if len(targets) == 0 {
				continue
			}
			node := nodes[service]
			dependencies := setupGet(node, "depends_on")
			if dependencies == nil {
				dependencies = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				setupPut(node, "depends_on", dependencies)
			}
			if dependencies.Kind == yaml.SequenceNode {
				converted := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				for _, entry := range dependencies.Content {
					var value yaml.Node
					value.Encode(map[string]string{"condition": "service_started"})
					setupPut(converted, entry.Value, &value)
				}
				*dependencies = *converted
			}
			if dependencies.Kind != yaml.MappingNode {
				return fmt.Errorf("advanced depends_on definition needs the Compose editor")
			}
			for target := range targets {
				if setupGet(dependencies, target) != nil {
					continue
				}
				condition := "service_started"
				health := setupGet(nodes[target], "healthcheck")
				if health != nil && setupString(setupGet(health, "disable")) != "true" {
					condition = "service_healthy"
				}
				var value yaml.Node
				value.Encode(map[string]string{"condition": condition})
				setupPut(dependencies, target, &value)
			}
		}
	}
	return nil
}
