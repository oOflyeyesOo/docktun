package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"docktun/internal/db"
	"gopkg.in/yaml.v3"
)

type CloudflareAPI struct {
	db     *db.DB
	client *http.Client
}

type CloudflareCredentials struct {
	APIToken  string `json:"api_token"`
	AccountID string `json:"account_id"`
	ZoneID    string `json:"zone_id"`
	HasToken  bool   `json:"has_token"`
}

type cfApiResponse struct {
	Success  bool              `json:"success"`
	Errors   []cfApiError      `json:"errors"`
	Messages []string          `json:"messages"`
	Result   json.RawMessage   `json:"result"`
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

// FindZoneID resolves the Cloudflare Zone ID for a given domain/hostname
func (s *CloudflareAPI) FindZoneID(token, hostname string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(hostname))
	parts := strings.Split(clean, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid hostname: %s", hostname)
	}

	// Try candidate domain names from longest to shortest (e.g. sub.domain.co.uk -> domain.co.uk -> co.uk)
	for i := 0; i <= len(parts)-2; i++ {
		candidate := strings.Join(parts[i:], ".")
		req, err := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/zones?name="+candidate+"&status=active", nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		var cfResp cfApiResponse
		if err := json.NewDecoder(resp.Body).Decode(&cfResp); err == nil && cfResp.Success {
			var zones []cfZone
			if err := json.Unmarshal(cfResp.Result, &zones); err == nil && len(zones) > 0 {
				return zones[0].ID, nil
			}
		}
	}

	return "", fmt.Errorf("could not find active Cloudflare zone for %s", hostname)
}

// SyncDNSRecord creates or updates a CNAME DNS record in Cloudflare pointing to <tunnelID>.cfargotunnel.com
func (s *CloudflareAPI) SyncDNSRecord(hostname, tunnelID string) error {
	cleanHost := cleanHostname(hostname)
	if cleanHost == "" || tunnelID == "" || tunnelID == "your-tunnel-uuid" {
		return fmt.Errorf("invalid hostname (%s) or tunnel ID (%s)", cleanHost, tunnelID)
	}

	target := fmt.Sprintf("%s.cfargotunnel.com", tunnelID)
	creds := s.GetCredentials()

	// 1. If API Token is available, use Cloudflare v4 REST API
	if creds.APIToken != "" {
		zoneID := creds.ZoneID
		var err error
		if zoneID == "" {
			zoneID, err = s.FindZoneID(creds.APIToken, cleanHost)
			if err != nil {
				return fmt.Errorf("resolve zone ID for %s: %w", cleanHost, err)
			}
		}

		// Check for existing record
		queryURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?name=%s", zoneID, cleanHost)
		req, err := http.NewRequest("GET", queryURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+creds.APIToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			return fmt.Errorf("query dns records: %w", err)
		}
		defer resp.Body.Close()

		var cfResp cfApiResponse
		if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
			return fmt.Errorf("parse cf response: %w", err)
		}
		if !cfResp.Success {
			errMsg := "unknown api error"
			if len(cfResp.Errors) > 0 {
				errMsg = cfResp.Errors[0].Message
			}
			return fmt.Errorf("cloudflare api error: %s", errMsg)
		}

		var existingRecords []cfDNSRecord
		_ = json.Unmarshal(cfResp.Result, &existingRecords)

		recordPayload := map[string]interface{}{
			"type":    "CNAME",
			"name":    cleanHost,
			"content": target,
			"proxied": true,
			"ttl":     1, // Auto
		}
		bodyBytes, _ := json.Marshal(recordPayload)

		if len(existingRecords) > 0 {
			// Record exists: update it
			rec := existingRecords[0]
			if rec.Type == "CNAME" && rec.Content == target && rec.Proxied {
				// Already correctly configured
				log.Printf("[docktun] Cloudflare DNS record %s already points to %s", cleanHost, target)
				return nil
			}

			updateURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, rec.ID)
			putReq, _ := http.NewRequest("PUT", updateURL, bytes.NewReader(bodyBytes))
			putReq.Header.Set("Authorization", "Bearer "+creds.APIToken)
			putReq.Header.Set("Content-Type", "application/json")

			putResp, err := s.client.Do(putReq)
			if err != nil {
				return fmt.Errorf("update dns record: %w", err)
			}
			defer putResp.Body.Close()

			var putCfResp cfApiResponse
			_ = json.NewDecoder(putResp.Body).Decode(&putCfResp)
			if !putCfResp.Success {
				errMsg := "failed to update dns record"
				if len(putCfResp.Errors) > 0 {
					errMsg = putCfResp.Errors[0].Message
				}
				return fmt.Errorf("cloudflare dns update: %s", errMsg)
			}
			log.Printf("[docktun] Cloudflare DNS record updated: %s -> %s", cleanHost, target)
			return nil
		}

		// Record does not exist: create it
		createURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID)
		postReq, _ := http.NewRequest("POST", createURL, bytes.NewReader(bodyBytes))
		postReq.Header.Set("Authorization", "Bearer "+creds.APIToken)
		postReq.Header.Set("Content-Type", "application/json")

		postResp, err := s.client.Do(postReq)
		if err != nil {
			return fmt.Errorf("create dns record: %w", err)
		}
		defer postResp.Body.Close()

		var postCfResp cfApiResponse
		_ = json.NewDecoder(postResp.Body).Decode(&postCfResp)
		if !postCfResp.Success {
			errMsg := "failed to create dns record"
			if len(postCfResp.Errors) > 0 {
				errMsg = postCfResp.Errors[0].Message
			}
			return fmt.Errorf("cloudflare dns create: %s", errMsg)
		}

		log.Printf("[docktun] Created Cloudflare DNS record: %s -> %s (Proxied)", cleanHost, target)
		return nil
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
		out, err := exec.Command("cloudflared", "tunnel", "route", "dns", tunnelID, cleanHost).CombinedOutput()
		if err == nil {
			log.Printf("[docktun] CLI route dns succeeded: %s -> %s", cleanHost, tunnelID)
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

	zoneID := creds.ZoneID
	var err error
	if zoneID == "" {
		zoneID, err = s.FindZoneID(creds.APIToken, clean)
		if err != nil {
			return fmt.Errorf("resolve zone ID: %w", err)
		}
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

