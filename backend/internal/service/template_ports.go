package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"docktun/internal/templateprobe"
	"gopkg.in/yaml.v3"
)

// Keep field creation and probe metadata on the same parser. Advanced ranges
// remain intact in Compose rather than borrowing the next editable field.
func setupPortBinding(entry *yaml.Node) (published, ip, protocol, target string, editable bool) {
	protocol = "tcp"
	if entry.Kind == yaml.MappingNode {
		published = setupString(setupGet(entry, "published"))
		target = setupString(setupGet(entry, "target"))
		ip = setupString(setupGet(entry, "host_ip"))
		if value := setupString(setupGet(entry, "protocol")); value != "" {
			protocol = value
		}
	} else if entry.Kind == yaml.ScalarNode {
		parts := setupMountParts(entry.Value)
		if len(parts) < 2 || len(parts) > 3 {
			return
		}
		published = parts[len(parts)-2]
		target = parts[len(parts)-1]
		if len(parts) == 3 {
			ip = strings.Trim(parts[0], "[]")
		}
		if value, tail, ok := strings.Cut(target, "/"); ok {
			target, protocol = value, tail
		}
	}
	port, err := strconv.Atoi(target)
	if err != nil || port < 1 || port > 65535 || (protocol != "tcp" && protocol != "udp") {
		return
	}
	if port, err = strconv.Atoi(published); err == nil {
		editable = port >= 0 && port <= 65535
	} else if match := setupInterpolation.FindStringSubmatch(published); match != nil {
		editable = match[0] == published && published != "$$"
	}
	return
}

func setupPortMetadata(root *yaml.Node, result *TemplateSetup) {
	services := setupGet(root, "services")
	for i := 0; i+1 < len(services.Content); i += 2 {
		service, node := services.Content[i].Value, services.Content[i+1]
		ports := setupGet(node, "ports")
		if ports == nil || ports.Kind != yaml.SequenceNode {
			continue
		}
		literalFields := []int{}
		for index, field := range result.Fields {
			if field.Kind == "port" && field.Service == service {
				literalFields = append(literalFields, index)
			}
		}
		next := 0
		for _, entry := range ports.Content {
			published, ip, protocol, _, editable := setupPortBinding(entry)
			if !editable {
				result.Notes = append(result.Notes, service+": advanced port ranges/protocols stay in the Compose editor")
				continue
			}
			index := -1
			matches := setupInterpolation.FindStringSubmatch(published)
			if matches != nil && matches[0] == published && matches[0] != "$$" {
				name := matches[1]
				if name == "" {
					name = matches[4]
				}
				for j, field := range result.Fields {
					if field.ID == "var:"+name {
						index = j
						break
					}
				}
			} else if _, err := strconv.Atoi(published); err == nil && next < len(literalFields) {
				index = literalFields[next]
				next++
			}
			if index < 0 {
				continue
			}
			result.Fields[index].Kind = "port"
			result.Fields[index].PortUses = append(result.Fields[index].PortUses, templateprobe.Use{IP: ip, Protocol: protocol})
			result.Fields[index].Hint = "Docker-host port; Find free ports checks Docker mappings and host listeners."
		}
	}
}

func runTemplateProbe(ctx context.Context, image, kind string, input any, source, user string, write bool, groups []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	name := fmt.Sprintf("docktun-template-check-%d", time.Now().UnixNano())
	args := []string{"run", "--rm", "-i", "--name", name, "--label", "docktun.storage-check=true", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"}
	if kind == "ports" {
		args = append(args, "--network", "host", "--cap-add", "NET_BIND_SERVICE")
	} else {
		args = append(args, "--network", "none")
		var mount strings.Builder
		writer := csv.NewWriter(&mount)
		parts := []string{"type=bind", "source=" + source, "target=/checked"}
		if !write {
			parts = append(parts, "readonly")
		}
		writer.Write(parts)
		writer.Flush()
		args = append(args, "--mount", strings.TrimSuffix(mount.String(), "\n"))
		if user != "" {
			args = append(args, "--user", user)
		}
		for _, group := range groups {
			args = append(args, "--group-add", group)
		}
	}
	args = append(args, "--entrypoint", "/app/template-helper", image, kind)
	cmd := exec.CommandContext(ctx, "docker", args...)
	if input != nil {
		raw, _ := json.Marshal(input)
		cmd.Stdin = bytes.NewReader(raw)
	}
	raw, err := cmd.Output()
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		exec.CommandContext(cleanup, "docker", "rm", "-f", name).Run()
		return nil, fmt.Errorf("Docker host probe unavailable; check host networking and Docker access")
	}
	return raw, nil
}

type TemplatePortResult struct {
	Values map[string]string `json:"values"`
	Notes  []string          `json:"notes"`
}

func FindTemplatePorts(ctx context.Context, input TemplateSetupInput) (TemplatePortResult, error) {
	result := TemplatePortResult{Values: map[string]string{}, Notes: []string{}}
	input.Apply = false
	schema, err := ConfigureTemplate(input)
	if err != nil {
		return result, err
	}
	own, err := selfContainer(ctx)
	if err != nil {
		return result, err
	}
	var containers []struct {
		Ports []struct {
			IP         string
			PublicPort int
			Type       string
		}
	}
	if err = dockerJSON(ctx, "GET", "/containers/json", nil, &containers); err != nil {
		return result, err
	}
	request := templateprobe.Request{}
	environment := map[string]string{}
	for _, field := range schema.Fields {
		if strings.HasPrefix(field.ID, "var:") {
			value := field.Value
			if edit, ok := input.Values[field.ID]; ok {
				value = edit
			}
			environment[strings.TrimPrefix(field.ID, "var:")] = value
		}
	}
	for _, container := range containers {
		for _, port := range container.Ports {
			if port.PublicPort > 0 {
				request.Used = append(request.Used, templateprobe.Use{IP: port.IP, Protocol: port.Type, Port: port.PublicPort})
			}
		}
	}
	for _, field := range schema.Fields {
		if len(field.PortUses) == 0 {
			continue
		}
		value := field.Value
		if edit, ok := input.Values[field.ID]; ok {
			value = edit
		}
		preferred, _ := strconv.Atoi(value)
		uses := append([]templateprobe.Use{}, field.PortUses...)
		for i := range uses {
			ip, resolveErr := resolveTemplatePath(uses[i].IP, environment)
			if resolveErr != nil {
				return result, fmt.Errorf("host bind address: %w", resolveErr)
			}
			ip = strings.Trim(ip, "[]")
			if ip != "" && net.ParseIP(ip) == nil {
				return result, fmt.Errorf("host bind address must be an IPv4 or IPv6 address")
			}
			uses[i].IP = ip
		}
		request.Groups = append(request.Groups, templateprobe.Group{ID: field.ID, Preferred: preferred, Uses: uses})
	}
	if len(request.Groups) == 0 {
		result.Notes = append(result.Notes, "No individually editable host ports were found. Docker can assign automatic host ports; advanced ranges stay in the Compose editor.")
		return result, nil
	}
	raw, err := runTemplateProbe(ctx, own.Image, "ports", request, "", "", false, nil)
	if err != nil {
		return result, err
	}
	var selected templateprobe.Result
	if json.Unmarshal(raw, &selected) != nil {
		return result, fmt.Errorf("invalid host port check response")
	}
	if len(selected.Errors) > 0 {
		return result, fmt.Errorf("%s", strings.Join(selected.Errors, "; "))
	}
	result.Values = selected.Ports
	// Only local public/base URLs are adjusted. Internal database URLs retain
	// their container ports, and shared ${PORT} variables update naturally.
	for _, field := range schema.Fields {
		name := strings.ToUpper(field.Name)
		if !strings.Contains(" PUBLIC_URL APP_URL BASE_URL ROOT_URL WEBUI_URL EXTERNAL_URL ", " "+name+" ") {
			continue
		}
		original := field.Value
		if edit, ok := input.Values[field.ID]; ok {
			original = edit
		}
		address, err := url.Parse(original)
		if err != nil || (address.Hostname() != "localhost" && address.Hostname() != "127.0.0.1") {
			continue
		}
		for _, port := range schema.Fields {
			if port.Service != field.Service || len(port.PortUses) == 0 {
				continue
			}
			old := port.Value
			if edit, ok := input.Values[port.ID]; ok {
				old = edit
			}
			if address.Port() == old && result.Values[port.ID] != old {
				address.Host = address.Hostname() + ":" + result.Values[port.ID]
				result.Values[field.ID] = address.String()
				result.Notes = append(result.Notes, "Updated "+field.Service+" "+field.Name+" to its selected host port.")
			}
		}
	}
	result.Notes = append(result.Notes, "Ports were free on the Docker host when checked. Deployment checks them again; the check does not reserve them.")
	return result, nil
}
