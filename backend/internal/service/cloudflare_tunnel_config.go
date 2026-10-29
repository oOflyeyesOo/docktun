package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type TunnelIdentity struct {
	ID        string `json:"tunnel_id"`
	AccountID string `json:"account_id,omitempty"`
	Remote    bool   `json:"remote_managed"`
}

type cloudflareDNSPartialError struct{ cause error }

func (e *cloudflareDNSPartialError) Error() string {
	return fmt.Sprintf("hostname route saved in Cloudflare, but DNS setup failed: %v; the API token needs Zone DNS Edit/Write for this hostname's zone (Account DNS Settings permission does not grant DNS record access)", e.cause)
}

func (e *cloudflareDNSPartialError) Unwrap() error { return e.cause }

// A connector token identifies a remotely managed tunnel. Never fall back to a
// stale local config when that token is present, and never return its secret.
func (m *ConfigManager) TunnelIdentity() (TunnelIdentity, error) {
	var identity TunnelIdentity
	data, err := os.ReadFile(filepath.Join(filepath.Dir(m.cfg.CloudflaredConfig), "tunnel.token"))
	if err != nil && !os.IsNotExist(err) {
		return identity, err
	}
	if len(bytes.TrimSpace(data)) > 0 {
		identity.Remote = true
		var payload struct {
			Account string `json:"a"`
			Tunnel  string `json:"t"`
			Secret  string `json:"s"`
		}
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			raw, decodeErr := encoding.DecodeString(strings.TrimSpace(string(data)))
			if decodeErr == nil && json.Unmarshal(raw, &payload) == nil && payload.Account != "" && payload.Tunnel != "" && payload.Secret != "" {
				identity.ID, identity.AccountID = payload.Tunnel, payload.Account
				return identity, nil
			}
		}
		return identity, fmt.Errorf("cannot read tunnel identity from the saved connector token; replace it with the tunnel connector token from Cloudflare")
	}
	raw, err := m.Read()
	if err != nil {
		return identity, err
	}
	var config struct {
		Tunnel string `yaml:"tunnel"`
	}
	if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
		return identity, err
	}
	identity.ID = strings.TrimSpace(config.Tunnel)
	if identity.ID == "YOUR_TUNNEL_ID" || identity.ID == "your-tunnel-uuid" {
		identity.ID = ""
	}
	return identity, nil
}

func (s *CloudflareAPI) tunnelRequest(identity TunnelIdentity, method, suffix string, payload any) (json.RawMessage, error) {
	credentials := s.GetCredentials()
	if credentials.APIToken == "" {
		return nil, fmt.Errorf("save a Cloudflare API token under Cloudflare → API credentials; the connector token cannot query or edit routes")
	}
	account := identity.AccountID
	if account == "" {
		account = credentials.AccountID
	}
	if credentials.AccountID != "" && identity.AccountID != "" && credentials.AccountID != identity.AccountID {
		return nil, fmt.Errorf("saved Cloudflare Account ID does not match this tunnel's connector token")
	}
	if account == "" || identity.ID == "" {
		return nil, fmt.Errorf("Cloudflare Account ID and tunnel ID are required")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	endpoint := "https://api.cloudflare.com/client/v4/accounts/" + url.PathEscape(account) + "/cfd_tunnel/" + url.PathEscape(identity.ID) + suffix
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credentials.APIToken)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Cloudflare tunnel request failed: %w", err)
	}
	defer response.Body.Close()
	var envelope cfApiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("invalid Cloudflare tunnel response (HTTP %d): %w", response.StatusCode, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		message := "request rejected"
		if len(envelope.Errors) > 0 {
			message = envelope.Errors[0].Message
		}
		return nil, fmt.Errorf("Cloudflare tunnel API (HTTP %d): %s; use Cloudflare Tunnel Read for status and Edit for route changes", response.StatusCode, message)
	}
	return envelope.Result, nil
}

func (s *CloudflareAPI) TunnelStatus(identity TunnelIdentity) (map[string]any, error) {
	raw, err := s.tunnelRequest(identity, http.MethodGet, "", nil)
	if err != nil {
		return nil, err
	}
	var status map[string]any
	err = json.Unmarshal(raw, &status)
	return status, err
}

func (s *CloudflareAPI) TunnelConfiguration(identity TunnelIdentity) (map[string]any, error) {
	version := s.tunnelMutation(identity)
	raw, err := s.tunnelRequest(identity, http.MethodGet, "/configurations", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Config == nil {
		return nil, fmt.Errorf("Cloudflare returned no tunnel configuration; existing routes were left unchanged")
	}
	s.rememberTunnelIngress(identity, result.Config, &version)
	return result.Config, nil
}

// Only mutate the selected app's hostname route. Preserve path rules, the
// existing catch-all, global origin settings and fields unknown to DockTun.
func (s *DockerContainers) syncRemoteCloudflareIngress(app string, next ExposureSettings, identity TunnelIdentity) error {
	if s.cfAPI == nil {
		return fmt.Errorf("Cloudflare API service is unavailable")
	}
	config, err := s.cfAPI.TunnelConfiguration(identity)
	if err != nil {
		return err
	}
	ingress, ok := config["ingress"].([]any)
	if !ok || len(ingress) == 0 {
		return fmt.Errorf("Cloudflare returned no ingress rules; existing configuration was left unchanged")
	}
	last, ok := ingress[len(ingress)-1].(map[string]any)
	if !ok || last["hostname"] != nil && last["hostname"] != "" || last["path"] != nil && last["path"] != "" || last["service"] == nil {
		return fmt.Errorf("Cloudflare configuration has no final catch-all rule; existing routes were left unchanged")
	}
	host := cleanHostname(next.Hostname)
	if next.Mode == "public" && host == "" {
		return fmt.Errorf("enter the public hostname to expose")
	}
	previous := s.getExposureFromDB(app)
	oldHost := ""
	if previous.Mode == "public" {
		oldHost = cleanHostname(previous.Hostname)
	}
	// A discovered remote route may have no SQLite row. Capture its actual
	// hostname/target from inventory, then verify it against this fresh response.
	actualHost, actualTarget := next.previousCloudflareHost, next.previousCloudflareTarget
	rules := make([]any, 0, len(ingress)+1)
	var selected map[string]any
	for _, item := range ingress[:len(ingress)-1] {
		rule, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid Cloudflare ingress rule; configuration was left unchanged")
		}
		ruleHost, _ := rule["hostname"].(string)
		path, _ := rule["path"].(string)
		service, _ := rule["service"].(string)
		storedMatch := oldHost != "" && cleanHostname(ruleHost) == oldHost && (service == previous.Target || previous.Target == "" && ingressContainer(service) == app)
		actualMatch := actualHost != "" && cleanHostname(ruleHost) == cleanHostname(actualHost) && service == actualTarget
		if path == "" && (storedMatch || actualMatch || next.Mode == "public" && cleanHostname(ruleHost) == host) {
			selected = rule
			continue
		}
		rules = append(rules, rule)
	}
	if next.Mode == "public" {
		if selected == nil {
			selected = map[string]any{}
		}
		selected["hostname"], selected["service"] = host, s.normalizeTarget(app, next.Target)
		rules = append(rules, selected)
	}
	rules = append(rules, last)
	config["ingress"] = rules
	if _, err := s.cfAPI.tunnelRequest(identity, http.MethodPut, "/configurations", map[string]any{"config": config}); err != nil {
		return err
	}
	// Even a later DNS failure must not hide a route already accepted by the
	// tunnel API. Keep the partial failure explicit while inventory stays true.
	s.cfAPI.rememberTunnelIngress(identity, config, nil)
	if next.Mode == "public" {
		if err := s.cfAPI.SyncDNSRecord(host, identity.ID); err != nil {
			return &cloudflareDNSPartialError{cause: err}
		}
	}
	return nil
}

// Used by DNS tools: use the same route source as the active connector.
func (s *CloudflareAPI) ActiveConfig(mgr *ConfigManager) (string, error) {
	identity, err := mgr.TunnelIdentity()
	if !identity.Remote {
		return mgr.Read()
	}
	if err != nil {
		return "", err
	}
	configuration, err := s.TunnelConfiguration(identity)
	if err != nil {
		return "", err
	}
	configuration["tunnel"] = identity.ID
	raw, err := yaml.Marshal(configuration)
	return string(raw), err
}
