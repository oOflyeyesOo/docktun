package service

import (
	"context"
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	var out []byte
	var err error
	if s.exposureDocker != nil {
		out, err = s.exposureDocker(ctx, args...)
	} else {
		out, err = exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	}
	if err != nil {
		operation := args[0]
		if len(args) > 1 && args[0] == "exec" {
			operation += " in " + args[1]
		}
		return nil, fmt.Errorf("Tailscale forwarding failed (%s): %s (%w)", operation, strings.TrimSpace(string(out)), err)
	}
	return out, nil
}
