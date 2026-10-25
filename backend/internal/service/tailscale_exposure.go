package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func tailscaleExposure(e ExposureSettings) bool {
	return e.Mode == "private" && e.PrivateType == "tailscale"
}
func tailscaleTarget(name string, e ExposureSettings) (string, error) {
	if e.PrivatePort < 1 || e.PrivatePort > 65535 {
		return "", fmt.Errorf("choose a Tailscale TCP port between 1 and 65535")
	}
	target := strings.TrimSpace(e.Target)
	if target == "" {
		return net.JoinHostPort(name, strconv.Itoa(e.PrivatePort)), nil
	}
	if !strings.Contains(target, "://") {
		target = "tcp://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("invalid container target")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Hostname() != name || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "tcp" && u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("target must be this container's name and internal TCP port, for example %s:8080", name)
	}
	return net.JoinHostPort(name, strconv.Itoa(port)), nil
}
func (s *DockerContainers) runExposure(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out []byte
	var err error
	if s.exposureDocker != nil {
		out, err = s.exposureDocker(ctx, args...)
	} else {
		out, err = exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	}
	if err != nil {
		return nil, fmt.Errorf("Tailscale forwarding failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return out, nil
}
func (s *DockerContainers) setTailscaleRule(port int, target string) error {
	args := []string{"exec", "docktun-tailscale", "tailscale", "serve", "--bg", "--tcp=" + strconv.Itoa(port)}
	if target == "" {
		args = append(args, "off")
	} else {
		args = append(args, "tcp://"+target)
	}
	_, err := s.runExposure(args...)
	return err
}

// Returns an undo operation so a failed database write doesn't leave an untracked rule.
func (s *DockerContainers) syncTailscaleExposure(name string, previous ExposureSettings, next *ExposureSettings) (func(), error) {
	noop := func() {}
	was, is := tailscaleExposure(previous), tailscaleExposure(*next)
	if !was && !is {
		return noop, nil
	}
	oldTarget := ""
	if was {
		oldTarget, _ = tailscaleTarget(name, previous)
	}
	newTarget := ""
	if is {
		var err error
		newTarget, err = tailscaleTarget(name, *next)
		if err != nil {
			return noop, err
		}
		if s.db != nil {
			var owner string
			err = s.db.QueryRow(`SELECT container_name FROM container_exposures WHERE mode='private' AND private_type IN ('tailscale','') AND private_port=? AND container_name<>?`, next.PrivatePort, name).Scan(&owner)
			if err == nil {
				return noop, fmt.Errorf("Tailscale port %d is already assigned to %s; choose another private port", next.PrivatePort, owner)
			}
		}
		out, err := s.runExposure("exec", "docktun-tailscale", "tailscale", "status", "--json")
		if err != nil {
			return noop, err
		}
		var status struct {
			BackendState string
			TailscaleIPs []string
			Self         struct {
				DNSName      string
				TailscaleIPs []string
			}
		}
		if json.Unmarshal(out, &status) != nil || status.BackendState != "Running" {
			return noop, fmt.Errorf("sign in to Tailscale in Remote Access before exposing a container")
		}
		next.PrivateHost = strings.TrimSuffix(status.Self.DNSName, ".")
		if next.PrivateHost == "" {
			ips := status.Self.TailscaleIPs
			if len(ips) == 0 {
				ips = status.TailscaleIPs
			}
			if len(ips) > 0 {
				next.PrivateHost = ips[0]
			}
		}
		next.PrivateType = "tailscale"
		next.Target = "tcp://" + newTarget
		// Avoid overwriting Serve rules created outside DockTun.
		out, err = s.runExposure("exec", "docktun-tailscale", "tailscale", "serve", "status", "--json")
		if err != nil {
			return noop, err
		}
		var config struct {
			TCP map[string]struct {
				TCPForward  string
				HTTP, HTTPS bool
			}
		}
		if err = json.Unmarshal(out, &config); err != nil {
			return noop, fmt.Errorf("cannot inspect existing Tailscale forwarding: %w", err)
		}
		if rule, exists := config.TCP[strconv.Itoa(next.PrivatePort)]; exists && (!was || previous.PrivatePort != next.PrivatePort || rule.TCPForward != oldTarget) {
			return noop, fmt.Errorf("Tailscale port %d already has a Serve rule; choose another private port", next.PrivatePort)
		}
		if err = s.setTailscaleRule(next.PrivatePort, newTarget); err != nil {
			return noop, err
		}
	}
	if was && (!is || previous.PrivatePort != next.PrivatePort) {
		if err := s.setTailscaleRule(previous.PrivatePort, ""); err != nil {
			if is {
				_ = s.setTailscaleRule(next.PrivatePort, "")
			}
			return noop, err
		}
	}
	return func() {
		if is {
			_ = s.setTailscaleRule(next.PrivatePort, "")
		}
		if was {
			_ = s.setTailscaleRule(previous.PrivatePort, oldTarget)
		}
	}, nil
}
