package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func (s *DockerContainers) runNetworkDockerWithContext(ctx context.Context, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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

func (s *DockerContainers) runNetworkDocker(args ...string) ([]byte, error) {
	return s.runNetworkDockerWithContext(context.Background(), args...)
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

// IngressReconcileResult contains the outcome of an ingress network reconciliation run.
type IngressReconcileResult struct {
	Network          string   `json:"network"`
	Connected        []string `json:"connected"`
	AlreadyConnected []string `json:"already_connected"`
	Skipped          []string `json:"skipped"`
}

func extractTargetContainer(target string) string {
	target = strings.TrimSpace(target)
	if target == "" || strings.HasPrefix(target, "http_status:") {
		return ""
	}
	raw := target
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "tcp" {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	if strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1" || host == "0.0.0.0" {
		return ""
	}
	if net.ParseIP(host) != nil {
		return ""
	}
	return host
}

// ReconcileIngressNetworks automatically reconciles Cloudflare ingress rules with Docker networks.
// Any running container referenced in an ingress rule target that is not yet attached to the
// connector network is safely attached via Docker network connect.
func (s *DockerContainers) ReconcileIngressNetworks(ctx context.Context) error {
	_, err := s.ReconcileIngressNetworksDetailed(ctx)
	return err
}

func (s *DockerContainers) ReconcileIngressNetworksDetailed(ctx context.Context) (*IngressReconcileResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.exposureMu.Lock()
	defer s.exposureMu.Unlock()

	network := s.exposureNetworkName()
	if network == "host" || network == "none" {
		return nil, fmt.Errorf("Cloudflare publishing requires a Docker bridge network shared with its connector")
	}
	netID, err := s.EnsureNetworkID(network)
	if err != nil {
		return nil, fmt.Errorf("ensure network %s: %w", network, err)
	}

	res := &IngressReconcileResult{
		Network:          network,
		Connected:        []string{},
		AlreadyConnected: []string{},
		Skipped:          []string{},
	}

	if s.configMgr == nil {
		return res, nil
	}

	var rawYaml string
	if s.cfAPI != nil {
		rawYaml, _ = s.cfAPI.ActiveConfig(s.configMgr)
	}
	if strings.TrimSpace(rawYaml) == "" {
		rawYaml, err = s.configMgr.Read()
		if err != nil {
			if os.IsNotExist(err) {
				return res, nil
			}
			return nil, fmt.Errorf("read cloudflare config: %w", err)
		}
	}
	if strings.TrimSpace(rawYaml) == "" {
		return res, nil
	}

	var doc struct {
		Ingress []struct {
			Hostname string `yaml:"hostname"`
			Service  string `yaml:"service"`
			Path     string `yaml:"path"`
		} `yaml:"ingress"`
	}
	if err := yaml.Unmarshal([]byte(rawYaml), &doc); err != nil {
		return nil, fmt.Errorf("unmarshal cloudflare config: %w", err)
	}

	var containers []string
	seen := make(map[string]bool)
	for _, rule := range doc.Ingress {
		c := extractTargetContainer(rule.Service)
		if c != "" && !seen[c] {
			seen[c] = true
			containers = append(containers, c)
		}
	}

	targetNet := netID
	if targetNet == "" {
		targetNet = network
	}

	for _, container := range containers {
		// 1. Check if container is running
		outRunning, err := s.runNetworkDockerWithContext(ctx, "inspect", "--format", "{{.State.Running}}", container)
		if err != nil {
			res.Skipped = append(res.Skipped, container)
			continue
		}
		if strings.TrimSpace(string(outRunning)) != "true" {
			res.Skipped = append(res.Skipped, container)
			continue
		}

		// 2. Check container network membership
		outNetworks, err := s.runNetworkDockerWithContext(ctx, "inspect", "--format", "{{json .NetworkSettings.Networks}}", container)
		if err != nil {
			res.Skipped = append(res.Skipped, container)
			continue
		}
		var networks map[string]struct {
			NetworkID string `json:"NetworkID"`
		}
		if err := json.Unmarshal(outNetworks, &networks); err != nil {
			res.Skipped = append(res.Skipped, container)
			continue
		}

		alreadyAttached := false
		for netName, membership := range networks {
			if netName == network || (netID != "" && membership.NetworkID == netID) {
				alreadyAttached = true
				break
			}
		}
		if alreadyAttached {
			res.AlreadyConnected = append(res.AlreadyConnected, container)
			continue
		}

		// 3. Connect container to network
		if _, err := s.runNetworkDockerWithContext(ctx, "network", "connect", targetNet, container); err != nil {
			if strings.Contains(err.Error(), "already exists in network") {
				res.AlreadyConnected = append(res.AlreadyConnected, container)
			} else {
				log.Printf("[docktun] auto-heal: failed to connect container %s to network %s: %v", container, targetNet, err)
				res.Skipped = append(res.Skipped, container)
			}
		} else {
			log.Printf("[docktun] auto-heal: safely connected container %s to network %s", container, network)
			res.Connected = append(res.Connected, container)
		}
	}

	return res, nil
}
