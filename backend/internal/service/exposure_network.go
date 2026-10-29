package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

func (s *DockerContainers) runNetworkDocker(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if s.networkDocker != nil {
		return s.networkDocker(ctx, args...)
	}
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Docker %s: %s (%w)", args[0], strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

func (s *DockerContainers) exposureNetworkName() string {
	if configured := strings.TrimSpace(os.Getenv("DOCKER_NETWORK")); configured != "" {
		if regexpNetworkID(configured) {
			if out, err := s.runNetworkDocker("network", "inspect", "--format", "{{.Name}}", configured); err == nil && strings.TrimSpace(string(out)) != "" {
				return strings.TrimSpace(string(out))
			}
		}
		return configured
	}
	// Existing Compose installations can use a scoped/custom connector network.
	// Reuse that bridge instead of creating a second network by a guessed name.
	connector := "docktun-cloudflared"
	if s.cfg != nil && s.cfg.CloudflaredContainer != "" {
		connector = s.cfg.CloudflaredContainer
	}
	if out, err := s.runNetworkDocker("inspect", "--format", "{{json .NetworkSettings.Networks}}", connector); err == nil {
		var networks map[string]json.RawMessage
		if json.Unmarshal(out, &networks) == nil {
			for _, preferred := range []string{"docktun", "cloudflared"} {
				if _, ok := networks[preferred]; ok {
					return preferred
				}
			}
			var names []string
			for name := range networks {
				if name != "host" && name != "none" && name != "bridge" {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			if len(names) == 1 {
				return names[0]
			}
		}
	}
	return s.DefaultNetwork()
}

func regexpNetworkID(name string) bool {
	if len(name) < 12 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// Return an undo for only the endpoint created by this save. Existing app and
// Compose networks are never disconnected or deleted by exposure rollback.
func (s *DockerContainers) connectExposureNetwork(app string) (func(), error) {
	noop := func() {}
	network := s.exposureNetworkName()
	if network == "host" || network == "none" {
		return noop, fmt.Errorf("Cloudflare publishing requires a Docker bridge network shared with its connector")
	}
	id, err := s.EnsureNetworkID(network)
	if err != nil {
		return noop, err
	}
	out, err := s.runNetworkDocker("inspect", "--format", "{{json .NetworkSettings.Networks}}", app)
	if err != nil {
		return noop, err
	}
	var networks map[string]struct{ NetworkID string }
	if err := json.Unmarshal(out, &networks); err != nil {
		return noop, fmt.Errorf("cannot inspect container network membership: %w", err)
	}
	for name, membership := range networks {
		if name == network || membership.NetworkID == id {
			return noop, nil
		}
	}
	if _, err = s.runNetworkDocker("network", "connect", id, app); err != nil {
		if strings.Contains(err.Error(), "already exists in network") {
			return noop, nil
		}
		return noop, err
	}
	return func() {
		if _, err := s.runNetworkDocker("network", "disconnect", id, app); err != nil {
			log.Printf("[docktun] roll back Cloudflare network for %s: %v", app, err)
		}
	}, nil
}
