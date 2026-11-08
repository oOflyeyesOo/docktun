package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type databaseLinkContainer struct {
	Name   string
	Config struct {
		Env    []string
		Labels map[string]string
	}
	State           struct{ Running bool }
	NetworkSettings struct {
		Networks map[string]struct{ Aliases []string }
	}
}

func databaseHosts(env []string) []string {
	hosts := map[string]bool{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(key) {
		case "DATABASE_URL", "DB_URL":
			u, err := url.Parse(value)
			if err != nil {
				continue
			}
			value = u.Hostname()
		case "DB_HOST", "POSTGRES_HOST", "PGHOST", "MYSQL_HOST", "MARIADB_HOST", "REDIS_HOST":
			if host, _, err := net.SplitHostPort(value); err == nil {
				value = host
			}
		default:
			continue
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && value != "localhost" && net.ParseIP(value) == nil {
			hosts[value] = true
		}
	}
	result := []string{}
	for host := range hosts {
		result = append(result, host)
	}
	sort.Strings(result)
	return result
}

// Restore only an explicit dependency's existing Docker DNS network. A missing
// dependency or an ambiguous match is diagnostic information, not permission to
// start a guessed database, change its aliases, or restart an app.
func databaseLinkNetworks(target databaseLinkContainer, candidates []databaseLinkContainer) []string {
	if target.Config.Labels[tailscaleOwnerLabel] != "" {
		return nil
	}
	selected := map[string]bool{}
	for _, host := range databaseHosts(target.Config.Env) {
		project := target.Config.Labels["com.docker.compose.project"]
		matches := []databaseLinkContainer{}
		for _, candidate := range candidates {
			if !candidate.State.Running || candidate.Name == target.Name || candidate.Config.Labels[tailscaleOwnerLabel] != "" {
				continue
			}
			sameProject := project != "" && candidate.Config.Labels["com.docker.compose.project"] == project
			explicitName := strings.EqualFold(strings.TrimPrefix(candidate.Name, "/"), host)
			serviceName := sameProject && strings.EqualFold(candidate.Config.Labels["com.docker.compose.service"], host)
			if explicitName || serviceName {
				matches = append(matches, candidate)
			}
		}
		if len(matches) != 1 {
			continue
		}
		for network, endpoint := range matches[0].NetworkSettings.Networks {
			if network == "bridge" || network == "host" || network == "none" || strings.HasPrefix(network, "docktun-ts-") {
				continue
			}
			if _, exists := target.NetworkSettings.Networks[network]; exists {
				continue
			}
			resolves := strings.EqualFold(strings.TrimPrefix(matches[0].Name, "/"), host)
			for _, alias := range endpoint.Aliases {
				if strings.EqualFold(alias, host) {
					resolves = true
				}
			}
			if resolves {
				selected[network] = true
			}
		}
	}
	result := []string{}
	for network := range selected {
		result = append(result, network)
	}
	sort.Strings(result)
	return result
}

func (s *DockerContainers) EnsureDatabaseLinks(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "inspect", id).Output()
	if err != nil {
		return err
	}
	var targets []databaseLinkContainer
	if json.Unmarshal(out, &targets) != nil || len(targets) != 1 {
		return fmt.Errorf("cannot inspect container dependency networks")
	}
	if targets[0].Config.Labels[tailscaleOwnerLabel] != "" || len(databaseHosts(targets[0].Config.Env)) == 0 {
		return nil
	}
	out, err = exec.CommandContext(ctx, "docker", "ps", "--format", "{{.ID}}").Output()
	if err != nil {
		return err
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil
	}
	out, err = exec.CommandContext(ctx, "docker", append([]string{"inspect"}, ids...)...).Output()
	if err != nil {
		return err
	}
	var candidates []databaseLinkContainer
	if err = json.Unmarshal(out, &candidates); err != nil {
		return err
	}
	for _, network := range databaseLinkNetworks(targets[0], candidates) {
		if _, err = exec.CommandContext(ctx, "docker", "network", "connect", network, id).CombinedOutput(); err != nil {
			return fmt.Errorf("restore dependency network %s: %w", network, err)
		}
	}
	return nil
}
