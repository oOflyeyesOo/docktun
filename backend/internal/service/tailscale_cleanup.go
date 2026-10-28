package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

type tailscaleRuleCleanup struct {
	App, Node, Target string
	Port              int
}

var errTailscaleNeedsConnection = errors.New("connect Tailscale before cleaning its previous routes")

// A login may finish after the start request returns. Retry only already
// running, connected devices; never enable an unused provider from the ticker.
func (s *DockerContainers) reconcilePendingTailscaleRules() {
	if s.db == nil {
		return
	}
	rows, err := s.db.Query(`SELECT app, node FROM tailscale_rule_cleanup`)
	if err != nil {
		return
	}
	nodes := map[string]string{}
	for rows.Next() {
		var app, node string
		if rows.Scan(&app, &node) == nil {
			nodes[node] = app
		}
	}
	rows.Close()
	for node, app := range nodes {
		_, running, err := s.tailscaleNodeState(app, node)
		if err != nil || !running {
			continue
		}
		out, err := s.runExposure("exec", node, "tailscale", "status", "--json")
		var status struct{ BackendState string }
		if err != nil || json.Unmarshal(out, &status) != nil || status.BackendState != "Running" {
			continue
		}
		if err := s.cleanPendingTailscaleRules(node); err != nil {
			log.Printf("[docktun] pending Tailscale cleanup in %s: %v", node, err)
		}
	}
}

// Inspect before exec: an absent or stopped provider cannot have an active
// forwarding rule. Only an explicitly enabled provider may be started.
func (s *DockerContainers) tailscaleNodeState(app, node string) (exists, running bool, err error) {
	out, err := s.runExposure("container", "inspect", node)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such container") || strings.Contains(strings.ToLower(err.Error()), "no such object") {
			return false, false, nil
		}
		return false, false, err
	}
	var nodes []struct {
		State  struct{ Running bool }
		Config struct{ Labels map[string]string }
	}
	if json.Unmarshal(out, &nodes) != nil || len(nodes) != 1 {
		return false, false, fmt.Errorf("cannot inspect Tailscale container %s", node)
	}
	if node != "docktun-tailscale" && nodes[0].Config.Labels[tailscaleOwnerLabel] != app {
		return false, false, fmt.Errorf("refusing to modify a Tailscale container not owned by this app")
	}
	return true, nodes[0].State.Running, nil
}

func (s *DockerContainers) pendingTailscaleRules(node string) ([]tailscaleRuleCleanup, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT app, node, port, target FROM tailscale_rule_cleanup WHERE node = ?`, node)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []tailscaleRuleCleanup
	for rows.Next() {
		var rule tailscaleRuleCleanup
		if err := rows.Scan(&rule.App, &rule.Node, &rule.Port, &rule.Target); err != nil {
			return nil, err
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

// Caller holds exposureMu. Never reset Serve or remove another app's rule.
func (s *DockerContainers) cleanPendingTailscaleRules(node string) error {
	rules, err := s.pendingTailscaleRules(node)
	if err != nil || len(rules) == 0 {
		return err
	}
	for _, rule := range rules {
		if node != "docktun-tailscale" && node != tailscaleNodeName(rule.App) {
			return fmt.Errorf("invalid stored Tailscale cleanup device")
		}
		_, running, err := s.tailscaleNodeState(rule.App, node)
		if err != nil {
			return err
		}
		if !running {
			return fmt.Errorf("Tailscale container %s is stopped; pending rule cleanup will run when it is enabled", node)
		}
		present, err := s.verifyTailscaleRule(node, rule.Port, rule.Target, true)
		if err != nil {
			return fmt.Errorf("clean previous Tailscale route for %s: %w", rule.App, err)
		}
		if present {
			if err := s.setTailscaleRule(node, rule.Port, ""); err != nil {
				return err
			}
		}
		if _, err := s.db.Exec(`DELETE FROM tailscale_rule_cleanup WHERE app = ? AND node = ? AND port = ? AND target = ?`, rule.App, node, rule.Port, rule.Target); err != nil {
			return err
		}
	}
	return nil
}

func (s *DockerContainers) waitPendingTailscaleRules(node string) error {
	rules, err := s.pendingTailscaleRules(node)
	if err != nil || len(rules) == 0 {
		return err
	}
	if err = s.waitTailscaleServe(node); err != nil {
		return err
	}
	return s.cleanPendingTailscaleRules(node)
}

func (s *DockerContainers) waitTailscaleServe(node string) error {
	// The socket can answer with an empty Serve configuration before the
	// persisted profile has loaded. Wait for the connected backend as well.
	var err error
	var status struct{ BackendState string }
	for attempt := 0; attempt < 20; attempt++ {
		var out []byte
		out, err = s.runExposure("exec", node, "tailscale", "status", "--json")
		if err == nil {
			if err = json.Unmarshal(out, &status); err != nil {
				return fmt.Errorf("invalid Tailscale backend status in %s: %w", node, err)
			}
			if status.BackendState == "Running" {
				if _, err = s.runExposure("exec", node, "tailscale", "serve", "status", "--json"); err == nil {
					return nil
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err == nil && (status.BackendState == "NeedsLogin" || status.BackendState == "Stopped") {
		return errTailscaleNeedsConnection
	}
	if err == nil {
		err = fmt.Errorf("backend is %s", status.BackendState)
	}
	return fmt.Errorf("cannot clean previous Tailscale routes in %s: %w", node, err)
}

// The general container controls can also explicitly enable a tunnel helper.
func (s *DockerContainers) finishTailscaleStart(id string) error {
	if s.db == nil {
		return nil
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tailscale_rule_cleanup`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	name, err := s.containerName(id)
	if err != nil {
		return err
	}
	if err = s.waitPendingTailscaleRules(name); err != nil {
		if errors.Is(err, errTailscaleNeedsConnection) {
			return nil // Leave the helper available for interactive sign-in.
		}
		_ = dockerCommand("stop", id)
	}
	return err
}

// A dormant app device keeps its identity, but must not retain connectivity to
// the app while its old persistent Serve rule awaits cleanup.
func (s *DockerContainers) detachTailscaleApp(app string) (bool, error) {
	node := tailscaleNodeName(app)
	out, err := s.runExposure("network", "inspect", node)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such network") || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return false, nil
		}
		return false, err
	}
	var networks []struct{ Labels map[string]string }
	if json.Unmarshal(out, &networks) != nil || len(networks) != 1 || networks[0].Labels[tailscaleOwnerLabel] != app {
		return false, fmt.Errorf("refusing to disconnect an unowned Tailscale network")
	}
	out, err = s.runExposure("container", "inspect", app)
	if err != nil {
		return false, err
	}
	var apps []struct {
		NetworkSettings struct{ Networks map[string]json.RawMessage }
	}
	if json.Unmarshal(out, &apps) != nil || len(apps) != 1 {
		return false, fmt.Errorf("cannot inspect app network")
	}
	if _, attached := apps[0].NetworkSettings.Networks[node]; !attached {
		return false, nil
	}
	_, err = s.runExposure("network", "disconnect", node, app)
	return err == nil, err
}
