package service

import (
	"bytes"
	"gopkg.in/yaml.v3"
	"os"
	"regexp"
	"strings"
	"time"
)

func yamlMapValue(node *yaml.Node, key string) *yaml.Node {
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

func repairComposeVolumes(content []byte) ([]byte, bool) {
	var doc yaml.Node
	if yaml.Unmarshal(content, &doc) != nil || len(doc.Content) != 1 {
		return content, false
	}
	var validation map[string]any
	if doc.Decode(&validation) != nil {
		return content, false
	}
	root := doc.Content[0]
	services := yamlMapValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return content, false
	}
	volumes := yamlMapValue(root, "volumes")
	if volumes != nil && volumes.Kind != yaml.MappingNode && volumes.Tag != "!!null" {
		return content, false
	}
	names := []string{}
	seen := map[string]bool{}
	validName := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	for i := 1; i < len(services.Content); i += 2 {
		mounts := yamlMapValue(services.Content[i], "volumes")
		if mounts == nil || mounts.Kind != yaml.SequenceNode {
			continue
		}
		for _, mount := range mounts.Content {
			source := ""
			if mount.Kind == yaml.ScalarNode {
				if len(mount.Value) > 2 && mount.Value[1] == ':' && (mount.Value[2] == '/' || mount.Value[2] == '\\') {
					continue
				}
				value, _, ok := strings.Cut(mount.Value, ":")
				if ok {
					source = value
				}
			} else if mount.Kind == yaml.MappingNode {
				kind := yamlMapValue(mount, "type")
				src := yamlMapValue(mount, "source")
				if kind != nil && kind.Value == "volume" && src != nil {
					source = src.Value
				}
			}
			if validName.MatchString(source) && yamlMapValue(volumes, source) == nil && !seen[source] {
				names = append(names, source)
				seen[source] = true
			}
		}
	}
	if len(names) == 0 {
		return content, false
	}
	if volumes == nil {
		volumes = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "volumes"}, volumes)
	}
	if volumes.Tag == "!!null" {
		volumes.Kind = yaml.MappingNode
		volumes.Tag = "!!map"
		volumes.Value = ""
	}
	for _, name := range names {
		volumes.Content = append(volumes.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle})
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if encoder.Encode(&doc) != nil {
		return content, false
	}
	return buffer.Bytes(), true
}

// Compose owns volume creation and host-path resolution. Repairing its document
// must not create container-only directories or accidentally turn file mounts into directories.
func SanitizeComposeVolumes(path string) bool {
	original, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	repaired, changed := repairComposeVolumes(original)
	if !changed {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if os.WriteFile(path+".bak-"+time.Now().UTC().Format("20060102T150405.000000000"), original, info.Mode().Perm()) != nil {
		return false
	}
	return os.WriteFile(path, repaired, info.Mode().Perm()) == nil
}
