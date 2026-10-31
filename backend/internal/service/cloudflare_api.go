package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"docktun/internal/db"
	"gopkg.in/yaml.v3"
)

type CloudflareAPI struct {
	db            *db.DB
	client        *http.Client
	tunnelCacheMu sync.Mutex
	tunnelCache   map[string]*tunnelIngressSnapshot
	tunnelWriteMu sync.Mutex
}

type CloudflareCredentials struct {
	APIToken  string `json:"api_token"`
	AccountID string `json:"account_id"`
	ZoneID    string `json:"zone_id"`
	HasToken  bool   `json:"has_token"`
}

type cfApiResponse struct {
	Success  bool            `json:"success"`
	Errors   []cfApiError    `json:"errors"`
	Messages []string        `json:"messages"`
	Result   json.RawMessage `json:"result"`
}

type cfApiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func formatCfErrorMessage(errors []cfApiError, defaultMsg, hostname string) string {
	msg := defaultMsg
	if len(errors) > 0 && errors[0].Message != "" {
		msg = errors[0].Message
	}
	if strings.Contains(strings.ToLower(msg), "authentication failed") {
		note := " (Verify that your Cloudflare API Token has 'Zone:DNS:Edit' permissions at dash.cloudflare.com/profile/api-tokens. A Tunnel Connector Token cannot manage DNS records)"
		if hostname != "" {
			note = fmt.Sprintf(" (Verify that your Cloudflare API Token has 'Zone:DNS:Edit' permissions for %s at dash.cloudflare.com/profile/api-tokens. A Tunnel Connector Token cannot manage DNS records)", hostname)
		}
		msg += note
	}
	return msg
}

func NewCloudflareAPI(db *db.DB) *CloudflareAPI {
	return &CloudflareAPI{
		db: db,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// GetCredentials returns saved Cloudflare credentials, automatically discovering
// and persisting them from credentials.json, token files, .env, and environment variables
func (s *CloudflareAPI) GetCredentials() CloudflareCredentials {
	var creds CloudflareCredentials
	dbHasToken := false
	dbHasAccount := false
	dbHasZone := false

	if s.db != nil {
		rows, err := s.db.Query("SELECT key, value FROM system_settings WHERE key IN ('cf_api_token', 'cf_account_id', 'cf_zone_id')")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var k, v string
				if err := rows.Scan(&k, &v); err == nil {
					switch k {
					case "cf_api_token":
						creds.APIToken = strings.TrimSpace(v)
						if creds.APIToken != "" {
							dbHasToken = true
						}
					case "cf_account_id":
						creds.AccountID = strings.TrimSpace(v)
						if creds.AccountID != "" {
							dbHasAccount = true
						}
					case "cf_zone_id":
						creds.ZoneID = strings.TrimSpace(v)
						if creds.ZoneID != "" {
							dbHasZone = true
						}
					}
				}
			}
		}
	}

	// 1. Check environment variables
	if creds.APIToken == "" {
		for _, envKey := range []string{"CF_API_TOKEN", "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_TOKEN", "CF_TOKEN"} {
			if val := strings.TrimSpace(os.Getenv(envKey)); val != "" {
				creds.APIToken = val
				break
			}
		}
	}
	if creds.AccountID == "" {
		for _, envKey := range []string{"CF_ACCOUNT_ID", "CLOUDFLARE_ACCOUNT_ID", "CF_ACCOUNT"} {
			if val := strings.TrimSpace(os.Getenv(envKey)); val != "" {
				creds.AccountID = val
				break
			}
		}
	}
	if creds.ZoneID == "" {
		for _, envKey := range []string{"CF_ZONE_ID", "CLOUDFLARE_ZONE_ID", "CF_ZONE"} {
			if val := strings.TrimSpace(os.Getenv(envKey)); val != "" {
				creds.ZoneID = val
				break
			}
		}
	}

	// 2. Check dedicated token files on disk
	if creds.APIToken == "" {
		tokenFiles := []string{
			"/etc/cloudflared/token",
			"/etc/cloudflared/api_token",
			"/etc/cloudflared/cf_api_token",
			"/etc/cloudflared/cf_token",
			"/app/cloudflared/token",
			"/app/cloudflared/api_token",
			"/app/data/cf_api_token",
			"./cloudflared/token",
			"./cloudflared/api_token",
			"./cloudflared/cf_api_token",
			"./cloudflared/cf_token",
		}
		if home := os.Getenv("HOME"); home != "" {
			tokenFiles = append(tokenFiles,
				filepath.Join(home, ".cloudflared", "token"),
				filepath.Join(home, ".cloudflared", "api_token"),
				filepath.Join(home, ".cloudflared", "cf_api_token"),
			)
		}
		for _, tf := range tokenFiles {
			if data, err := os.ReadFile(tf); err == nil {
				token := strings.TrimSpace(string(data))
				if token != "" && !strings.Contains(token, "\n") {
					creds.APIToken = token
					log.Printf("[docktun] Discovered Cloudflare API token from file: %s", tf)
					break
				}
			}
		}
	}

	// 3. Check credentials.json files (extracts AccountTag and optional api_token)
	credJsonPaths := []string{
		"/etc/cloudflared/credentials.json",
		"./cloudflared/credentials.json",
		"/app/cloudflared/credentials.json",
	}
	if home := os.Getenv("HOME"); home != "" {
		if matches, err := filepath.Glob(filepath.Join(home, ".cloudflared", "*.json")); err == nil {
			credJsonPaths = append(credJsonPaths, matches...)
		}
	}
	for _, cjp := range credJsonPaths {
		if data, err := os.ReadFile(cjp); err == nil {
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err == nil {
				// Check AccountTag / AccountID
				if creds.AccountID == "" {
					for _, k := range []string{"AccountTag", "account_tag", "AccountID", "account_id"} {
						if val, ok := raw[k].(string); ok && strings.TrimSpace(val) != "" {
							creds.AccountID = strings.TrimSpace(val)
							log.Printf("[docktun] Discovered Cloudflare Account ID from %s: %s", cjp, creds.AccountID)
							break
						}
					}
				}
				// Check api_token inside JSON
				if creds.APIToken == "" {
					for _, k := range []string{"api_token", "APIToken", "token", "Token", "cf_api_token"} {
						if val, ok := raw[k].(string); ok && strings.TrimSpace(val) != "" {
							creds.APIToken = strings.TrimSpace(val)
							log.Printf("[docktun] Discovered Cloudflare API token from %s", cjp)
							break
						}
					}
				}
			}
		}
	}

	// 4. Check .env files on disk
	if creds.APIToken == "" || creds.AccountID == "" || creds.ZoneID == "" {
		envPaths := []string{
			".env",
			"/.env",
			"/app/.env",
			"/opt/projects/docktun/.env",
		}
		for _, ep := range envPaths {
			if data, err := os.ReadFile(ep); err == nil {
				for _, line := range strings.Split(string(data), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
						continue
					}
					parts := strings.SplitN(line, "=", 2)
					k := strings.TrimSpace(parts[0])
					v := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
					if v == "" {
						continue
					}
					if creds.APIToken == "" && (k == "CF_API_TOKEN" || k == "CLOUDFLARE_API_TOKEN" || k == "CLOUDFLARE_TOKEN" || k == "CF_TOKEN") {
						creds.APIToken = v
						log.Printf("[docktun] Discovered Cloudflare API token from %s (%s)", ep, k)
					}
					if creds.AccountID == "" && (k == "CF_ACCOUNT_ID" || k == "CLOUDFLARE_ACCOUNT_ID" || k == "CF_ACCOUNT") {
						creds.AccountID = v
						log.Printf("[docktun] Discovered Cloudflare Account ID from %s (%s)", ep, k)
					}
					if creds.ZoneID == "" && (k == "CF_ZONE_ID" || k == "CLOUDFLARE_ZONE_ID" || k == "CF_ZONE") {
						creds.ZoneID = v
						log.Printf("[docktun] Discovered Cloudflare Zone ID from %s (%s)", ep, k)
					}
				}
			}
		}
	}

	creds.HasToken = creds.APIToken != ""

	// Auto-save discovered values to SQLite so they are permanently preserved across reboots
	if s.db != nil && ((creds.APIToken != "" && !dbHasToken) || (creds.AccountID != "" && !dbHasAccount) || (creds.ZoneID != "" && !dbHasZone)) {
		go func(tok, acc, zone string) {
			_ = s.SaveCredentials(tok, acc, zone)
		}(creds.APIToken, creds.AccountID, creds.ZoneID)
	}

	return creds
}

// SaveCredentials persists Cloudflare credentials into SQLite
func (s *CloudflareAPI) SaveCredentials(token, accountID, zoneID string) error {
	token = strings.TrimSpace(token)
	if isConnectorToken(token) {
		return fmt.Errorf("the token provided is a Cloudflare Tunnel Connector Token, which connects the cloudflared daemon but cannot manage DNS records via REST API. Please save this token under 'Tunnel connector token' at the top of the Cloudflare settings, and use a Cloudflare User API Token (from dash.cloudflare.com/profile/api-tokens with Zone:DNS:Edit permissions) here")
	}

	if s.db == nil {
		return fmt.Errorf("database not initialized")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	upsert := `INSERT INTO system_settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`

	if token != "" {
		if _, err := tx.Exec(upsert, "cf_api_token", strings.TrimSpace(token)); err != nil {
			return err
		}
	}
	if accountID != "" {
		if _, err := tx.Exec(upsert, "cf_account_id", strings.TrimSpace(accountID)); err != nil {
			return err
		}
	}
	if zoneID != "" {
		if _, err := tx.Exec(upsert, "cf_zone_id", strings.TrimSpace(zoneID)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// SaveSettings persists Cloudflare account, API token, and tunnel metadata into SQLite
func (s *CloudflareAPI) SaveSettings(accountID, token, tunnelID, tunnelName string) error {
	if err := s.SaveCredentials(token, accountID, ""); err != nil {
		return err
	}
	if s.db != nil {
		upsert := `INSERT INTO system_settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`
		if tunnelID != "" {
			_, _ = s.db.Exec(upsert, "cf_tunnel_id", strings.TrimSpace(tunnelID))
		}
		if tunnelName != "" {
			_, _ = s.db.Exec(upsert, "cf_tunnel_name", strings.TrimSpace(tunnelName))
		}
	}
	return nil
}

// FindZoneID resolves the Cloudflare Zone ID for a given domain/hostname
func (s *CloudflareAPI) FindZoneID(token, hostname string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(hostname))
	parts := strings.Split(clean, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid hostname: %s", hostname)
	}
	for i := 0; i <= len(parts)-2; i++ {
		candidate := strings.Join(parts[i:], ".")
		response, err := s.dnsRequest(http.MethodGet, "https://api.cloudflare.com/client/v4/zones?name="+url.QueryEscape(candidate)+"&status=active", token, hostname, nil)
		if err != nil {
			return "", fmt.Errorf("zone lookup: %w", err)
		}
		var zones []cfZone
		if err := json.Unmarshal(response.Result, &zones); err != nil {
			return "", fmt.Errorf("invalid Cloudflare zone response")
		}
		if len(zones) > 0 {
			return zones[0].ID, nil
		}
	}
	return "", fmt.Errorf("could not find active Cloudflare zone for %s; enter the domain's Zone ID in API credentials, or optionally grant Zone Read (Zone:Zone:Read) for discovery", hostname)
}

type DNSCheckResult struct {
	Hostname            string        `json:"hostname"`
	CleanHost           string        `json:"clean_host"`
	ZoneID              string        `json:"zone_id"`
	TunnelID            string        `json:"tunnel_id"`
	ExpectedTarget      string        `json:"expected_target"`
	Exists              bool          `json:"exists"`
	IsConfiguredCorrect bool          `json:"is_configured_correct"`
	HasConflict         bool          `json:"has_conflict"`
	ConflictDetails     string        `json:"conflict_details,omitempty"`
	Records             []cfDNSRecord `json:"records"`
}

type DNSReplaceResult struct {
	Hostname       string   `json:"hostname"`
	ZoneID         string   `json:"zone_id"`
	DeletedRecords []string `json:"deleted_records"`
	CreatedTarget  string   `json:"created_target"`
	Success        bool     `json:"success"`
	Message        string   `json:"message"`
}

// CheckDNSRecord checks live Cloudflare DNS records for a given hostname
func (s *CloudflareAPI) CheckDNSRecord(hostname, tunnelID string) (*DNSCheckResult, error) {
	cleanHost := cleanHostname(hostname)
	if cleanHost == "" {
		return nil, fmt.Errorf("invalid hostname: %s", hostname)
	}

	creds := s.GetCredentials()
	if creds.APIToken == "" {
		return nil, fmt.Errorf("Cloudflare API Token required to inspect DNS records")
	}

	zoneID, err := s.resolveDNSZone(creds, cleanHost)
	if err != nil {
		return nil, err
	}

	expectedTarget := fmt.Sprintf("%s.cfargotunnel.com", tunnelID)

	queryURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?name=%s", zoneID, cleanHost)
	req, err := http.NewRequest("GET", queryURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query dns records: %w", err)
	}
	defer resp.Body.Close()

	var cfResp cfApiResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, fmt.Errorf("parse cf response: %w", err)
	}
	if !cfResp.Success {
		errMsg := "unknown api error"
		if len(cfResp.Errors) > 0 {
			errMsg = cfResp.Errors[0].Message
		}
		return nil, fmt.Errorf("cloudflare api error: %s", errMsg)
	}

	var existingRecords []cfDNSRecord
	_ = json.Unmarshal(cfResp.Result, &existingRecords)

	res := &DNSCheckResult{
		Hostname:       hostname,
		CleanHost:      cleanHost,
		ZoneID:         zoneID,
		TunnelID:       tunnelID,
		ExpectedTarget: expectedTarget,
		Exists:         len(existingRecords) > 0,
		Records:        existingRecords,
	}

	if len(existingRecords) == 0 {
		res.ConflictDetails = "No existing DNS record found in Cloudflare."
		return res, nil
	}

	for _, rec := range existingRecords {
		if rec.Type == "CNAME" && strings.EqualFold(rec.Content, expectedTarget) && rec.Proxied {
			res.IsConfiguredCorrect = true
		} else {
			res.HasConflict = true
			if strings.HasSuffix(strings.ToLower(rec.Content), ".cfargotunnel.com") {
				res.ConflictDetails = fmt.Sprintf("Points to another Cloudflare Tunnel (%s) [%s]", rec.Content, rec.Type)
			} else {
				res.ConflictDetails = fmt.Sprintf("Points to another destination (%s) [%s]", rec.Content, rec.Type)
			}
		}
	}

	return res, nil
}

// ForceReplaceDNSRecord deletes any existing conflicting DNS records and creates the fresh CNAME
func (s *CloudflareAPI) ForceReplaceDNSRecord(hostname, tunnelID string) (*DNSReplaceResult, error) {
	return s.replaceTunnelDNS(hostname, tunnelID)
}

// SyncDNSRecord creates or updates a CNAME DNS record in Cloudflare pointing to <tunnelID>.cfargotunnel.com.
// If an existing record points to an older tunnel or conflicting IP, it automatically overwrites or replaces it.
func (s *CloudflareAPI) SyncDNSRecord(hostname, tunnelID string) error {
	cleanHost := cleanHostname(hostname)
	if cleanHost == "" || tunnelID == "" || tunnelID == "your-tunnel-uuid" {
		return fmt.Errorf("invalid hostname (%s) or tunnel ID (%s)", cleanHost, tunnelID)
	}

	target := fmt.Sprintf("%s.cfargotunnel.com", tunnelID)
	creds := s.GetCredentials()

	if creds.APIToken != "" {
		_, err := s.replaceTunnelDNS(hostname, tunnelID)
		return err
	}

	// 2. Fallback: Check if host or container cloudflared CLI has cert.pem
	certPaths := []string{
		"/etc/cloudflared/cert.pem",
		"/root/.cloudflared/cert.pem",
	}
	hasCert := false
	for _, cp := range certPaths {
		if _, err := os.Stat(cp); err == nil {
			hasCert = true
			break
		}
	}

	if hasCert {
		out, err := exec.Command("cloudflared", "tunnel", "route", "dns", "--overwrite-dns", tunnelID, cleanHost).CombinedOutput()
		if err == nil {
			log.Printf("[docktun] CLI route dns --overwrite-dns succeeded: %s -> %s", cleanHost, tunnelID)
			return nil
		}
		log.Printf("[docktun] CLI route dns note: %s", strings.TrimSpace(string(out)))
	}

	return fmt.Errorf("no Cloudflare API token configured. Please save your API Token in Cloudflare Settings or add a CNAME for %s -> %s in Cloudflare Dashboard", cleanHost, target)
}

// CreateWildcardRoute creates a wildcard CNAME record (*.domain.com -> <tunnelID>.cfargotunnel.com)
func (s *CloudflareAPI) CreateWildcardRoute(domain, tunnelID string) error {
	creds := s.GetCredentials()
	if creds.APIToken == "" {
		return fmt.Errorf("Cloudflare API Token required to create wildcard DNS")
	}

	clean := strings.ToLower(strings.TrimSpace(domain))
	clean = strings.TrimPrefix(clean, "*.")
	target := fmt.Sprintf("%s.cfargotunnel.com", tunnelID)

	zoneID, err := s.resolveDNSZone(creds, clean)
	if err != nil {
		return err
	}

	wildcardName := "*." + clean

	// Create or update wildcard record
	recordPayload := map[string]interface{}{
		"type":    "CNAME",
		"name":    wildcardName,
		"content": target,
		"proxied": true,
		"ttl":     1,
	}
	bodyBytes, _ := json.Marshal(recordPayload)

	createURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID)
	postReq, _ := http.NewRequest("POST", createURL, bytes.NewReader(bodyBytes))
	postReq.Header.Set("Authorization", "Bearer "+creds.APIToken)
	postReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(postReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var cfResp cfApiResponse
	_ = json.NewDecoder(resp.Body).Decode(&cfResp)
	if !cfResp.Success {
		// If error is code 81057 (Record already exists), that is a success
		for _, e := range cfResp.Errors {
			if e.Code == 81057 || strings.Contains(strings.ToLower(e.Message), "already exists") {
				return nil
			}
		}
		errMsg := "failed to create wildcard dns record"
		if len(cfResp.Errors) > 0 {
			errMsg = cfResp.Errors[0].Message
		}
		return fmt.Errorf("%s", errMsg)
	}

	return nil
}

// GetRemoteTunnel queries the Cloudflare API for remote tunnel edge status and connectors
func (s *CloudflareAPI) GetRemoteTunnel(tunnelID string) (map[string]interface{}, error) {
	creds := s.GetCredentials()
	if creds.APIToken == "" || creds.AccountID == "" {
		return nil, fmt.Errorf("Cloudflare API Token and Account ID required")
	}

	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/cfd_tunnel/%s", creds.AccountID, tunnelID)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare api request failed: %w", err)
	}
	defer resp.Body.Close()

	var cfResp cfApiResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, fmt.Errorf("parse cloudflare response: %w", err)
	}
	if !cfResp.Success {
		errMsg := "cloudflare api query failed"
		if len(cfResp.Errors) > 0 {
			errMsg = cfResp.Errors[0].Message
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(cfResp.Result, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// AutoSyncIngressDNS automatically reads the active cloudflared config.yml,
// synchronizes every ingress hostname to Cloudflare DNS CNAME records,
// and ensures a wildcard DNS route (*.domain.com) is in place.
func (s *CloudflareAPI) AutoSyncIngressDNS(configMgr *ConfigManager) {
	if configMgr == nil {
		return
	}

	rawYaml, err := configMgr.Read()
	if err != nil || strings.TrimSpace(rawYaml) == "" {
		return
	}

	var doc struct {
		Tunnel  string `yaml:"tunnel"`
		Ingress []struct {
			Hostname string `yaml:"hostname"`
			Service  string `yaml:"service"`
		} `yaml:"ingress"`
	}

	if err := yaml.Unmarshal([]byte(rawYaml), &doc); err != nil {
		log.Printf("[docktun] auto-dns: failed to parse config.yml: %v", err)
		return
	}

	tid := strings.TrimSpace(doc.Tunnel)
	if tid == "" || tid == "YOUR_TUNNEL_ID" || tid == "your-tunnel-uuid" {
		return
	}

	log.Printf("[docktun] auto-dns: starting automated Cloudflare DNS sync for tunnel %s...", tid)

	var detectedRootDomain string
	for _, rule := range doc.Ingress {
		host := strings.TrimSpace(rule.Hostname)
		if host == "" || strings.HasPrefix(rule.Service, "http_status:") {
			continue
		}

		// Sync individual hostname CNAME
		if err := s.SyncDNSRecord(host, tid); err != nil {
			log.Printf("[docktun] auto-dns: sync notice for %s: %v", host, err)
		} else {
			log.Printf("[docktun] auto-dns: verified CNAME %s -> %s.cfargotunnel.com", host, tid)
		}

		if detectedRootDomain == "" {
			parts := strings.Split(strings.ToLower(host), ".")
			if len(parts) >= 2 {
				detectedRootDomain = strings.Join(parts[len(parts)-2:], ".")
			}
		}
	}

	// Also ensure a Wildcard route (*.domain.com) exists for the root domain
	if detectedRootDomain != "" && !strings.Contains(detectedRootDomain, "example.com") {
		if err := s.CreateWildcardRoute(detectedRootDomain, tid); err != nil {
			log.Printf("[docktun] auto-dns: wildcard sync notice (*.%s): %v", detectedRootDomain, err)
		} else {
			log.Printf("[docktun] auto-dns: verified wildcard CNAME *.%s -> %s.cfargotunnel.com", detectedRootDomain, tid)
		}
	}
}

// ==============================================================================
// Cloudflare Zero Trust Access Applications & Policies
// ==============================================================================

type AccessApplication struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Domain          string `json:"domain"`
	Type            string `json:"type"`
	SessionDuration string `json:"session_duration"`
	CreatedAt       string `json:"created_at,omitempty"`
}

func (s *CloudflareAPI) getAccessBaseURL(creds CloudflareCredentials, hostname string) (string, error) {
	if creds.AccountID != "" {
		return fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/access/apps", creds.AccountID), nil
	}
	zoneID := creds.ZoneID
	if zoneID == "" && hostname != "" {
		var err error
		zoneID, err = s.FindZoneID(creds.APIToken, hostname)
		if err != nil {
			return "", err
		}
	}
	if zoneID != "" {
		return fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/access/apps", zoneID), nil
	}
	return "", fmt.Errorf("Cloudflare Account ID or Zone ID required for Zero Trust Access")
}

// ListAccessApplications lists self-hosted Zero Trust Access applications
func (s *CloudflareAPI) ListAccessApplications() ([]AccessApplication, error) {
	creds := s.GetCredentials()
	if creds.APIToken == "" {
		return nil, fmt.Errorf("Cloudflare API token not configured")
	}

	baseURL, err := s.getAccessBaseURL(creds, "")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query zero trust apps: %w", err)
	}
	defer resp.Body.Close()

	var cfResp cfApiResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, fmt.Errorf("parse zero trust response: %w", err)
	}
	if !cfResp.Success {
		errMsg := "failed to list access applications"
		if len(cfResp.Errors) > 0 {
			errMsg = cfResp.Errors[0].Message
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	var apps []AccessApplication
	if err := json.Unmarshal(cfResp.Result, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

type ZeroTrustPolicyOptions struct {
	Hostname        string `json:"hostname"`
	AuthType        string `json:"auth_type"`        // "email", "domain", "everyone", "ip", "bypass_ip"
	Emails          string `json:"emails"`           // comma/space/newline separated emails
	Domains         string `json:"domains"`          // comma/space/newline separated domains
	IPRanges        string `json:"ip_ranges"`        // comma/space/newline separated IP CIDRs
	SessionDuration string `json:"session_duration"` // "15m", "30m", "1h", "6h", "12h", "24h", "7d", "30d"
}

// SyncZeroTrustAccessSimple provides backwards-compatible access for simple email strings
func (s *CloudflareAPI) SyncZeroTrustAccessSimple(hostname, emails string) (string, error) {
	return s.SyncZeroTrustAccess(ZeroTrustPolicyOptions{
		Hostname:        hostname,
		Emails:          emails,
		AuthType:        "email",
		SessionDuration: "24h",
	})
}

// SyncZeroTrustAccess creates or updates a Cloudflare Zero Trust Access Application
// and applies an Allow/Bypass Policy for the specified auth method (Email OTP, Domain, Everyone, IP Whitelist, IP Bypass).
func (s *CloudflareAPI) SyncZeroTrustAccess(opts ZeroTrustPolicyOptions) (string, error) {
	cleanHost := cleanHostname(opts.Hostname)
	if cleanHost == "" {
		return "", fmt.Errorf("invalid hostname for zero trust: %s", opts.Hostname)
	}

	creds := s.GetCredentials()
	if creds.APIToken == "" {
		return "", fmt.Errorf("Cloudflare API token required for Zero Trust Access")
	}

	baseURL, err := s.getAccessBaseURL(creds, cleanHost)
	if err != nil {
		return "", err
	}

	sessionDur := strings.TrimSpace(opts.SessionDuration)
	if sessionDur == "" {
		sessionDur = "24h"
	}

	authType := strings.ToLower(strings.TrimSpace(opts.AuthType))
	if authType == "" {
		authType = "email"
	}

	// 1. Check if application for domain already exists
	var appID string
	existingApps, err := s.ListAccessApplications()
	if err == nil {
		for _, a := range existingApps {
			if strings.EqualFold(cleanHostname(a.Domain), cleanHost) {
				appID = a.ID
				break
			}
		}
	}

	// 2. If app doesn't exist, create it; if it does, update session duration
	if appID == "" {
		createBody := map[string]interface{}{
			"name":                      cleanHost + " (DockTun)",
			"domain":                    cleanHost,
			"type":                      "self_hosted",
			"session_duration":          sessionDur,
			"auto_redirect_to_identity": false,
		}
		jsonBytes, _ := json.Marshal(createBody)
		req, err := http.NewRequest("POST", baseURL, bytes.NewBuffer(jsonBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+creds.APIToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("create access app: %w", err)
		}
		defer resp.Body.Close()

		var cfResp cfApiResponse
		if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
			return "", fmt.Errorf("parse create access app response: %w", err)
		}
		if !cfResp.Success {
			errMsg := "failed to create access application"
			if len(cfResp.Errors) > 0 {
				errMsg = cfResp.Errors[0].Message
			}
			return "", fmt.Errorf("%s", errMsg)
		}

		var created AccessApplication
		if err := json.Unmarshal(cfResp.Result, &created); err == nil && created.ID != "" {
			appID = created.ID
		}
	} else {
		// Update existing application settings (session duration)
		updateBody := map[string]interface{}{
			"name":             cleanHost + " (DockTun)",
			"session_duration": sessionDur,
		}
		jsonBytes, _ := json.Marshal(updateBody)
		updateURL := fmt.Sprintf("%s/%s", baseURL, appID)
		if putReq, err := http.NewRequest("PUT", updateURL, bytes.NewBuffer(jsonBytes)); err == nil {
			putReq.Header.Set("Authorization", "Bearer "+creds.APIToken)
			putReq.Header.Set("Content-Type", "application/json")
			if pResp, err := s.client.Do(putReq); err == nil {
				pResp.Body.Close()
			}
		}
	}

	if appID == "" {
		return "", fmt.Errorf("unable to determine application ID for %s", cleanHost)
	}

	// 3. Configure Access Policy on application
	policyURL := fmt.Sprintf("%s/%s/policies", baseURL, appID)
	decision := "allow"
	if authType == "bypass_ip" {
		decision = "bypass"
	}

	var include []map[string]interface{}

	splitTokens := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool {
			return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
		})
	}

	switch authType {
	case "everyone":
		// Any authenticated user who completes IdP login
		include = append(include, map[string]interface{}{
			"everyone": map[string]interface{}{},
		})

	case "domain":
		// Allowed email domain(s)
		domains := splitTokens(opts.Domains)
		if len(domains) == 0 {
			domains = splitTokens(opts.Emails)
		}
		for _, d := range domains {
			d = strings.TrimSpace(strings.TrimPrefix(d, "@"))
			if d != "" {
				include = append(include, map[string]interface{}{
					"email_domain": map[string]string{"domain": d},
				})
			}
		}

	case "ip", "bypass_ip":
		// IP or CIDR network range
		ips := splitTokens(opts.IPRanges)
		if len(ips) == 0 {
			ips = splitTokens(opts.Emails)
		}
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			if !strings.Contains(ip, "/") {
				if strings.Contains(ip, ":") {
					ip = ip + "/128"
				} else {
					ip = ip + "/32"
				}
			}
			include = append(include, map[string]interface{}{
				"ip": map[string]string{"ip": ip},
			})
		}

	case "email":
		fallthrough
	default:
		// Email OTP or domain
		rawEmails := splitTokens(opts.Emails)
		for _, em := range rawEmails {
			em = strings.TrimSpace(em)
			if em == "" {
				continue
			}
			if strings.HasPrefix(em, "@") {
				include = append(include, map[string]interface{}{
					"email_domain": map[string]string{"domain": strings.TrimPrefix(em, "@")},
				})
			} else if strings.Contains(em, "@") {
				include = append(include, map[string]interface{}{
					"email": map[string]string{"email": em},
				})
			} else if strings.Contains(em, ".") {
				include = append(include, map[string]interface{}{
					"email_domain": map[string]string{"domain": em},
				})
			}
		}
	}

	// Fallback to everyone if no specific rules were extracted
	if len(include) == 0 {
		include = append(include, map[string]interface{}{
			"everyone": map[string]interface{}{},
		})
	}

	policyBody := map[string]interface{}{
		"name":     "DockTun Zero Trust Policy",
		"decision": decision,
		"include":  include,
	}
	pBytes, _ := json.Marshal(policyBody)

	// Check if a policy already exists
	getPolReq, _ := http.NewRequest("GET", policyURL, nil)
	getPolReq.Header.Set("Authorization", "Bearer "+creds.APIToken)
	getPolReq.Header.Set("Content-Type", "application/json")
	if pResp, err := s.client.Do(getPolReq); err == nil {
		defer pResp.Body.Close()
		var polListResp cfApiResponse
		if err := json.NewDecoder(pResp.Body).Decode(&polListResp); err == nil && polListResp.Success {
			var pols []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(polListResp.Result, &pols); err == nil && len(pols) > 0 {
				// Update existing policy
				updatePolURL := fmt.Sprintf("%s/%s", policyURL, pols[0].ID)
				putReq, _ := http.NewRequest("PUT", updatePolURL, bytes.NewBuffer(pBytes))
				putReq.Header.Set("Authorization", "Bearer "+creds.APIToken)
				putReq.Header.Set("Content-Type", "application/json")
				if putResp, err := s.client.Do(putReq); err == nil {
					defer putResp.Body.Close()
					return appID, nil
				}
			}
		}
	}

	// Otherwise, create new policy
	createPolReq, err := http.NewRequest("POST", policyURL, bytes.NewBuffer(pBytes))
	if err == nil {
		createPolReq.Header.Set("Authorization", "Bearer "+creds.APIToken)
		createPolReq.Header.Set("Content-Type", "application/json")
		if cResp, err := s.client.Do(createPolReq); err == nil {
			defer cResp.Body.Close()
		}
	}

	return appID, nil
}

// DeleteZeroTrustAccess removes a Cloudflare Zero Trust Access Application
func (s *CloudflareAPI) DeleteZeroTrustAccess(appID string) error {
	if strings.TrimSpace(appID) == "" {
		return nil
	}
	creds := s.GetCredentials()
	if creds.APIToken == "" {
		return fmt.Errorf("Cloudflare API token not configured")
	}

	baseURL, err := s.getAccessBaseURL(creds, "")
	if err != nil {
		return err
	}

	deleteURL := fmt.Sprintf("%s/%s", baseURL, appID)
	req, err := http.NewRequest("DELETE", deleteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+creds.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete access app: %w", err)
	}
	defer resp.Body.Close()

	return nil
}
