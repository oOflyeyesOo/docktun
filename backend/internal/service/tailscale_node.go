package service

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

const tailscaleOwnerLabel = "io.docktun.tailscale-app"

// Names are stable across app recreation, but cannot collide after sanitizing names.
func tailscaleNodeName(app string) string {
	return fmt.Sprintf("docktun-ts-%x", sha256.Sum256([]byte(app)))[:31]
}

type ContainerTailscaleStatus struct {
	Node    string `json:"node"`
	State   string `json:"state"`
	AuthURL string `json:"auth_url,omitempty"`
	Address string `json:"address,omitempty"`
}

// Reattach only explicitly exposed apps after Compose recreates them. Never
// create a device, start a stopped helper, or connect an unrelated container.
func (s *DockerContainers) ReconcileTailscaleAppNetworks() {
	s.exposureMu.Lock()
	defer s.exposureMu.Unlock()
	s.reconcilePendingTailscaleRules()
	for app, exposure := range s.loadAllExposuresFromDB() {
		node := tailscaleNodeName(app)
		if !tailscaleExposure(exposure) || exposure.TailscaleNode != node {
			continue
		}
		if exists, err := s.ownedTailscaleNode(app); err != nil || !exists {
			continue
		}
		out, err := s.runExposure("network", "inspect", node)
		var networks []struct{ Labels map[string]string }
		if err != nil || json.Unmarshal(out, &networks) != nil || len(networks) != 1 || networks[0].Labels[tailscaleOwnerLabel] != app {
			continue
		}
		out, err = s.runExposure("container", "inspect", app)
		var apps []struct {
			State           struct{ Running bool }
			HostConfig      struct{ NetworkMode string }
			NetworkSettings struct{ Networks map[string]json.RawMessage }
		}
		if err != nil || json.Unmarshal(out, &apps) != nil || len(apps) != 1 || !apps[0].State.Running {
			continue
		}
		if apps[0].HostConfig.NetworkMode == "host" || strings.HasPrefix(apps[0].HostConfig.NetworkMode, "container:") {
			continue
		}
		if _, connected := apps[0].NetworkSettings.Networks[node]; !connected {
			_, _ = s.runExposure("network", "connect", node, app)
		}
	}
}

func (s *DockerContainers) ownedTailscaleNode(app string) (bool, error) {
	exists, _, err := s.tailscaleNodeState(app, tailscaleNodeName(app))
	return exists, err
}

func (s *DockerContainers) nodeStatus(app string) (ContainerTailscaleStatus, error) {
	result := ContainerTailscaleStatus{Node: tailscaleNodeName(app), State: "NotCreated"}
	exists, err := s.ownedTailscaleNode(app)
	if err != nil || !exists {
		return result, err
	}
	out, err := s.runExposure("exec", result.Node, "tailscale", "status", "--json")
	if err != nil {
		return result, err
	}
	var status struct {
		BackendState, AuthURL string
		TailscaleIPs          []string
		Self                  struct{ DNSName string }
	}
	if err = json.Unmarshal(out, &status); err != nil {
		return result, err
	}
	result.State, result.AuthURL = status.BackendState, status.AuthURL
	result.Address = strings.TrimSuffix(status.Self.DNSName, ".")
	if result.Address == "" && len(status.TailscaleIPs) > 0 {
		result.Address = status.TailscaleIPs[0]
	}
	return result, nil
}

// A private bridge contains only this app and its userspace Tailscale proxy.
// No Docker socket, host port, TUN device, route advertisement or NET_ADMIN is given to the proxy.
func (s *DockerContainers) ensureNodeResources(app string) error {
	node := tailscaleNodeName(app)
	for _, kind := range []string{"network", "volume"} {
		resource := node
		if kind == "volume" {
			resource += "-state"
		}
		out, err := s.runExposure(kind, "inspect", resource)
		if err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "no such") && !strings.Contains(err.Error(), "not found") {
				return err
			}
			if _, err = s.runExposure(kind, "create", "--label", tailscaleOwnerLabel+"="+app, resource); err != nil {
				return err
			}
		} else {
			var resources []struct{ Labels map[string]string }
			if json.Unmarshal(out, &resources) != nil || len(resources) != 1 || resources[0].Labels[tailscaleOwnerLabel] != app {
				return fmt.Errorf("%s %s is not owned by this app", kind, resource)
			}
		}
	}
	// Inspect membership first: Docker reports an error for an already connected endpoint.
	out, err := s.runExposure("container", "inspect", app)
	if err != nil {
		return err
	}
	var apps []struct {
		State           struct{ Running bool }
		HostConfig      struct{ NetworkMode string }
		NetworkSettings struct{ Networks map[string]json.RawMessage }
	}
	if json.Unmarshal(out, &apps) != nil || len(apps) != 1 {
		return fmt.Errorf("cannot inspect app network")
	}
	if !apps[0].State.Running {
		return fmt.Errorf("start the app before connecting its Tailscale device")
	}
	if apps[0].HostConfig.NetworkMode == "host" || strings.HasPrefix(apps[0].HostConfig.NetworkMode, "container:") {
		return fmt.Errorf("per-app Tailscale requires an app with its own Docker bridge network")
	}
	exists, err := s.ownedTailscaleNode(app)
	if err != nil {
		return err
	}
	if exists {
		_, err = s.runExposure("start", node)
	} else {
		_, err = s.runExposure("run", "-d", "--name", node, "--label", tailscaleOwnerLabel+"="+app,
			"--restart", "unless-stopped", "--network", node, "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
			"-v", node+"-state:/var/lib/tailscale", "--entrypoint", "tailscaled", "tailscale/tailscale:stable",
			"--tun=userspace-networking", "--state=/var/lib/tailscale/tailscaled.state")
	}
	if err != nil {
		return err
	}
	// Clean dormant rules before the app can be reached from this helper again.
	if err = s.waitPendingTailscaleRules(node); err != nil {
		if errors.Is(err, errTailscaleNeedsConnection) {
			return nil // Sign-in can proceed; keep the app detached until cleanup.
		}
		return err
	}
	if _, connected := apps[0].NetworkSettings.Networks[node]; !connected {
		_, err = s.runExposure("network", "connect", node, app)
	}
	return err
}

func (s *DockerContainers) ContainerTailscale(id string, connect bool, userID int, ip string) (ContainerTailscaleStatus, error) {
	s.exposureMu.Lock()
	defer s.exposureMu.Unlock()
	app, err := s.containerName(id)
	if err != nil {
		return ContainerTailscaleStatus{}, err
	}
	if connect {
		if err = s.ensureNodeResources(app); err != nil {
			return ContainerTailscaleStatus{}, err
		}
	}
	status, err := s.nodeStatus(app)
	if !connect {
		return status, err
	}
	// The daemon can need a moment to create its control socket on first start.
	for attempt := 0; (err != nil || status.State == "Starting" || status.State == "NoState") && attempt < 20; attempt++ {
		time.Sleep(300 * time.Millisecond)
		status, err = s.nodeStatus(app)
	}
	if err != nil {
		return status, err
	}
	if status.State == "NeedsLogin" || status.State == "Stopped" {
		// Interactive login is per app. No reusable account key is stored in Docker configuration.
		_, upErr := s.runExposure("exec", status.Node, "tailscale", "up", "--timeout=5s", "--reset", "--hostname="+app,
			"--accept-dns=false", "--accept-routes=false", "--advertise-routes=", "--advertise-exit-node=false", "--ssh=false")
		status, err = s.nodeStatus(app)
		if err == nil && status.State != "Running" && status.AuthURL == "" {
			err = upErr
		}
	}
	if err == nil && s.audit != nil {
		s.audit.Log(userID, "container_tailscale_connect", "Prepared dedicated Tailscale device for "+app, ip)
	}
	return status, err
}

func (s *DockerContainers) setTailscaleRule(node string, port int, target string) error {
	args := []string{"exec", node, "tailscale", "serve", "--bg", "--tcp=" + strconv.Itoa(port)}
	if target == "" {
		args = append(args, "off")
	} else {
		args = append(args, "tcp://"+target)
	}
	_, err := s.runExposure(args...)
	return err
}

// Existing shared-device rules are migrated only when the user explicitly saves
// that app. Never reset the shared daemon or touch another app's Serve rule.
func (s *DockerContainers) verifyTailscaleRule(node string, port int, target string, expected bool) (bool, error) {
	out, err := s.runExposure("exec", node, "tailscale", "serve", "status", "--json")
	if err != nil {
		return false, err
	}
	var config struct {
		TCP map[string]struct{ TCPForward string }
	}
	if err = json.Unmarshal(out, &config); err != nil {
		return false, err
	}
	if rule, ok := config.TCP[strconv.Itoa(port)]; ok && (!expected || rule.TCPForward != target) {
		return false, fmt.Errorf("Tailscale port %d has an unrelated Serve rule; it was left unchanged", port)
	}
	_, exists := config.TCP[strconv.Itoa(port)]
	return exists, nil
}

func (s *DockerContainers) syncTailscaleExposure(app string, previous ExposureSettings, next *ExposureSettings) (func(), error) {
	noop := func() {}
	was, is := tailscaleExposure(previous), tailscaleExposure(*next)
	next.TailscaleNode = "" // Never trust client-supplied helper identity.
	next.cleanup = nil
	if !was && !is {
		return noop, nil
	}
	oldNode := previous.TailscaleNode
	if oldNode == "" {
		oldNode = "docktun-tailscale"
	}
	if oldNode != "docktun-tailscale" && oldNode != tailscaleNodeName(app) {
		return noop, fmt.Errorf("invalid stored Tailscale device")
	}
	oldTarget := ""
	if was {
		oldTarget, _ = tailscaleTarget(app, previous)
	}
	node := tailscaleNodeName(app)
	newTarget := ""
	if is {
		var err error
		newTarget, err = tailscaleTarget(app, *next)
		if err != nil {
			return noop, err
		}
		status, err := s.nodeStatus(app)
		if err != nil {
			return noop, err
		}
		if status.State != "Running" {
			return noop, fmt.Errorf("connect and sign in this app's Tailscale device before saving")
		}
		if err = s.ensureNodeResources(app); err != nil {
			return noop, err
		}
		if _, err = s.verifyTailscaleRule(node, next.PrivatePort, oldTarget, was && oldNode == node && previous.PrivatePort == next.PrivatePort); err != nil {
			return noop, err
		}
		next.TailscaleNode = node
		next.PrivateHost = status.Address
		next.Target = "tcp://" + newTarget
	}
	oldRulePresent := false
	oldRunning := false
	if was {
		var err error
		_, oldRunning, err = s.tailscaleNodeState(app, oldNode)
		if err != nil {
			return noop, err
		}
		if oldRunning {
			oldRulePresent, err = s.verifyTailscaleRule(oldNode, previous.PrivatePort, oldTarget, true)
			if err != nil {
				return noop, err
			}
		} else {
			// Publish to another provider without starting or exec-ing a dormant
			// Tailscale container. Its exact old rule is cleaned on re-enable.
			next.cleanup = &tailscaleRuleCleanup{App: app, Node: oldNode, Port: previous.PrivatePort, Target: oldTarget}
		}
	}
	if is {
		if err := s.setTailscaleRule(node, next.PrivatePort, newTarget); err != nil {
			return noop, err
		}
	}
	if oldRulePresent && (!is || oldNode != node || previous.PrivatePort != next.PrivatePort) {
		if err := s.setTailscaleRule(oldNode, previous.PrivatePort, ""); err != nil {
			if is {
				_ = s.setTailscaleRule(node, next.PrivatePort, "")
			}
			return noop, err
		}
	}
	// A disconnected device retains its named state volume, preserving identity on re-enable.
	detached := false
	if !is && oldNode == node {
		var err error
		detached, err = s.detachTailscaleApp(app)
		if err != nil {
			if oldRulePresent {
				_ = s.setTailscaleRule(oldNode, previous.PrivatePort, oldTarget)
			}
			return noop, err
		}
		if oldRunning {
			_, err = s.runExposure("stop", node)
		}
		if err != nil {
			if detached {
				_, _ = s.runExposure("network", "connect", node, app)
			}
			if oldRulePresent {
				_ = s.setTailscaleRule(oldNode, previous.PrivatePort, oldTarget)
			}
			return noop, err
		}
	}
	return func() {
		if is {
			_ = s.setTailscaleRule(node, next.PrivatePort, "")
		}
		if was && oldNode == node && oldRunning {
			if _, err := s.runExposure("start", node); err == nil {
				if err := s.waitTailscaleServe(node); err != nil {
					log.Printf("[docktun] Tailscale rollback for %s: %v", app, err)
				}
			} else {
				log.Printf("[docktun] Tailscale rollback for %s: %v", app, err)
			}
		}
		if oldRulePresent {
			_ = s.setTailscaleRule(oldNode, previous.PrivatePort, oldTarget)
		}
		if detached {
			_, _ = s.runExposure("network", "connect", node, app)
		}
	}, nil
}
