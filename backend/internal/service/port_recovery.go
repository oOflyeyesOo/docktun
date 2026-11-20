package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DesiredPort represents a host port and protocol requested by a service or container.
type DesiredPort struct {
	HostIP        string `json:"host_ip,omitempty"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol,omitempty"` // "tcp" or "udp" (defaults to "tcp")
	ContainerName string `json:"container_name,omitempty"`
}

// PortCollision describes a collision between a desired port and an active host port/container.
type PortCollision struct {
	Port          int    `json:"port"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"host_ip,omitempty"`
	OccupiedBy    string `json:"occupied_by"` // container name(s) or "host process"
	ContainerName string `json:"container_name,omitempty"`
}

// IsPortConflictError checks whether an error string or message indicates a Docker/system port collision.
func IsPortConflictError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "port is already allocated") ||
		strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "failed to bind port") ||
		strings.Contains(msg, "bind: address already in use")
}

// RetryOnPortConflict executes fn with exponential backoff if a port conflict / allocation error occurs.
// This handles transient docker-proxy TIME_WAIT socket lag during container restarts.
func RetryOnPortConflict(ctx context.Context, fn func() error, maxAttempts int, delay time.Duration) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}

	var lastErr error
	currentDelay := delay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}

		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err
		if !IsPortConflictError(err) || attempt == maxAttempts {
			return err
		}

		// Wait with backoff before retrying to allow TIME_WAIT sockets to drain
		select {
		case <-time.After(currentDelay):
		case <-ctx.Done():
			return ctx.Err()
		}

		// Increase delay with bounded backoff
		currentDelay = currentDelay * 2
		if currentDelay > 5*time.Second {
			currentDelay = 5 * time.Second
		}
	}

	return lastErr
}

// ipMatchesCollision returns true if desiredIP and occupiedIP conflict on the host interface.
func ipMatchesCollision(desiredIP, occupiedIP string) bool {
	desired := strings.TrimSpace(desiredIP)
	occupied := strings.TrimSpace(occupiedIP)

	if desired == "" || desired == "0.0.0.0" || desired == "::" ||
		occupied == "" || occupied == "0.0.0.0" || occupied == "::" {
		return true
	}
	return desired == occupied
}

// ExtractDesiredPortsFromComposeContent parses a docker-compose YAML string or map structure and extracts all desired host port mappings.
func ExtractDesiredPortsFromComposeContent(composeContent string) []DesiredPort {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(composeContent), &document); err != nil {
		return nil
	}
	return ExtractDesiredPortsFromMap(document)
}

// ExtractDesiredPortsFromMap extracts all desired host port mappings from a parsed compose map.
func ExtractDesiredPortsFromMap(document map[string]any) []DesiredPort {
	services, _ := document["services"].(map[string]any)
	if len(services) == 0 {
		return nil
	}

	var desired []DesiredPort
	for serviceName, svcVal := range services {
		service, _ := svcVal.(map[string]any)
		containerName, _ := service["container_name"].(string)
		if containerName == "" {
			containerName = serviceName
		}

		ports, _ := service["ports"].([]any)
		for _, pVal := range ports {
			switch p := pVal.(type) {
			case map[string]any:
				// Long syntax: mode, target, published, protocol, host_ip
				publishedStr := ""
				switch pub := p["published"].(type) {
				case string:
					publishedStr = pub
				case int:
					publishedStr = strconv.Itoa(pub)
				case float64:
					publishedStr = strconv.Itoa(int(pub))
				}
				num, err := strconv.Atoi(publishedStr)
				if err != nil || num <= 0 {
					continue
				}
				proto, _ := p["protocol"].(string)
				if proto == "" {
					proto = "tcp"
				}
				hostIP, _ := p["host_ip"].(string)
				desired = append(desired, DesiredPort{
					HostIP:        hostIP,
					HostPort:      num,
					Protocol:      proto,
					ContainerName: containerName,
				})
			case string:
				// Short syntax: e.g. "8080:80", "127.0.0.1:8080:80", "80:80/udp"
				if parsed, ok := parsePortMapping(p); ok {
					hostNum, err := strconv.Atoi(parsed.HostPort)
					if err == nil && hostNum > 0 {
						proto := "tcp"
						if strings.HasSuffix(strings.ToLower(p), "/udp") {
							proto = "udp"
						}
						desired = append(desired, DesiredPort{
							HostIP:        parsed.HostIP,
							HostPort:      hostNum,
							Protocol:      proto,
							ContainerName: containerName,
						})
					}
				}
			case int:
				// Single port: target and host might be same or just target, but if mapped as int:
				if p > 0 {
					desired = append(desired, DesiredPort{
						HostPort:      p,
						Protocol:      "tcp",
						ContainerName: containerName,
					})
				}
			}
		}
	}

	return desired
}
func CheckPortCollisions(ctx context.Context, ports []DesiredPort) ([]PortCollision, error) {
	if len(ports) == 0 {
		return nil, nil
	}

	// 1. Query running containers via Docker API
	var running []struct {
		ID     string   `json:"Id"`
		Names  []string `json:"Names"`
		Labels map[string]string `json:"Labels"`
		Ports  []struct {
			IP          string `json:"IP"`
			PrivatePort int    `json:"PrivatePort"`
			PublicPort  int    `json:"PublicPort"`
			Type        string `json:"Type"`
		} `json:"Ports"`
	}

	if err := dockerJSON(ctx, "GET", "/containers/json", nil, &running); err != nil {
		return nil, fmt.Errorf("failed to list running containers: %w", err)
	}

	var collisions []PortCollision
	seen := make(map[string]bool)

	for _, desired := range ports {
		if desired.HostPort <= 0 {
			continue
		}
		proto := strings.ToLower(strings.TrimSpace(desired.Protocol))
		if proto == "" {
			proto = "tcp"
		}

		for _, container := range running {
			// Skip if it's the exact same container (by name)
			isSelf := false
			for _, n := range container.Names {
				cleanName := strings.TrimPrefix(n, "/")
				if desired.ContainerName != "" && cleanName == desired.ContainerName {
					isSelf = true
					break
				}
			}
			if isSelf {
				continue
			}

			for _, p := range container.Ports {
				containerProto := strings.ToLower(strings.TrimSpace(p.Type))
				if containerProto == "" {
					containerProto = "tcp"
				}

				if p.PublicPort == desired.HostPort && containerProto == proto {
					if ipMatchesCollision(desired.HostIP, p.IP) {
						names := make([]string, 0, len(container.Names))
						for _, n := range container.Names {
							names = append(names, strings.TrimPrefix(n, "/"))
						}
						occupiedBy := strings.Join(names, ", ")
						if occupiedBy == "" {
							occupiedBy = container.ID
							if len(occupiedBy) > 12 {
								occupiedBy = occupiedBy[:12]
							}
						}

						collisionKey := fmt.Sprintf("%d/%s/%s/%s", desired.HostPort, proto, desired.HostIP, occupiedBy)
						if !seen[collisionKey] {
							seen[collisionKey] = true
							collisions = append(collisions, PortCollision{
								Port:          desired.HostPort,
								Protocol:      proto,
								HostIP:        desired.HostIP,
								OccupiedBy:    occupiedBy,
								ContainerName: desired.ContainerName,
							})
						}
					}
				}
			}
		}
	}

	return collisions, nil
}
