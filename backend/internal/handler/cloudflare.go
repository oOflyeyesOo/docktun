package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"docktun/internal/service"
	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

type CloudflareHandler struct {
	cfAPI     *service.CloudflareAPI
	configMgr *service.ConfigManager
}

func NewCloudflareHandler(cfAPI *service.CloudflareAPI, configMgr *service.ConfigManager) *CloudflareHandler {
	return &CloudflareHandler{
		cfAPI:     cfAPI,
		configMgr: configMgr,
	}
}

type cfSettingsResponse struct {
	AccountID   string `json:"account_id"`
	ZoneID      string `json:"zone_id"`
	HasToken    bool   `json:"has_token"`
	MaskedToken string `json:"masked_token"`
	APIToken    string `json:"api_token"`
}

func (h *CloudflareHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	creds := h.cfAPI.GetCredentials()
	identity, _ := h.configMgr.TunnelIdentity()
	if creds.AccountID == "" && identity.Remote {
		creds.AccountID = identity.AccountID
	}
	masked := ""
	if len(creds.APIToken) > 8 {
		masked = creds.APIToken[:4] + "••••••••" + creds.APIToken[len(creds.APIToken)-4:]
	} else if len(creds.APIToken) > 0 {
		masked = "••••••••"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfSettingsResponse{
		AccountID:   creds.AccountID,
		ZoneID:      creds.ZoneID,
		HasToken:    creds.HasToken,
		MaskedToken: masked,
		APIToken:    creds.APIToken,
	})
}

func (h *CloudflareHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIToken  string `json:"api_token"`
		AccountID string `json:"account_id"`
		ZoneID    string `json:"zone_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if err := h.cfAPI.SaveCredentials(req.APIToken, req.AccountID, req.ZoneID); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Cloudflare credentials saved to database",
	})
}

func (h *CloudflareHandler) SyncAllRoutes(w http.ResponseWriter, r *http.Request) {
	if h.configMgr == nil {
		http.Error(w, `{"error":"config manager not ready"}`, http.StatusInternalServerError)
		return
	}

	rawYaml, err := h.cfAPI.ActiveConfig(h.configMgr)
	if err != nil {
		http.Error(w, `{"error":"failed to read config: `+err.Error()+`"}`, http.StatusInternalServerError)
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
		http.Error(w, `{"error":"failed to parse config.yml"}`, http.StatusBadRequest)
		return
	}

	if doc.Tunnel == "" || doc.Tunnel == "YOUR_TUNNEL_ID" || doc.Tunnel == "your-tunnel-uuid" {
		http.Error(w, `{"error":"no valid tunnel ID found in config.yml"}`, http.StatusBadRequest)
		return
	}

	type syncResult struct {
		Hostname string `json:"hostname"`
		Success  bool   `json:"success"`
		Error    string `json:"error,omitempty"`
	}

	force := r.URL.Query().Get("force") == "true"
	var results []syncResult
	for _, rule := range doc.Ingress {
		host := strings.TrimSpace(rule.Hostname)
		if host == "" || rule.Service == "http_status:404" {
			continue
		}

		var err error
		if force {
			_, err = h.cfAPI.ForceReplaceDNSRecord(host, doc.Tunnel)
		} else {
			err = h.cfAPI.SyncDNSRecord(host, doc.Tunnel)
		}

		if err != nil {
			results = append(results, syncResult{Hostname: host, Success: false, Error: err.Error()})
		} else {
			results = append(results, syncResult{Hostname: host, Success: true})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"tunnel":  doc.Tunnel,
		"synced":  results,
	})
}

func (h *CloudflareHandler) CheckDNS(w http.ResponseWriter, r *http.Request) {
	hostname := strings.TrimSpace(r.URL.Query().Get("hostname"))
	if hostname == "" {
		http.Error(w, `{"error":"hostname query parameter required"}`, http.StatusBadRequest)
		return
	}

	rawYaml, err := h.cfAPI.ActiveConfig(h.configMgr)
	if err != nil {
		http.Error(w, `{"error":"failed to read config"}`, http.StatusInternalServerError)
		return
	}
	var doc struct {
		Tunnel string `yaml:"tunnel"`
	}
	_ = yaml.Unmarshal([]byte(rawYaml), &doc)

	check, err := h.cfAPI.CheckDNSRecord(hostname, doc.Tunnel)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(check)
}

func (h *CloudflareHandler) ReplaceDNS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Hostname) == "" {
		http.Error(w, `{"error":"valid hostname required"}`, http.StatusBadRequest)
		return
	}

	rawYaml, err := h.cfAPI.ActiveConfig(h.configMgr)
	if err != nil {
		http.Error(w, `{"error":"failed to read config"}`, http.StatusInternalServerError)
		return
	}
	var doc struct {
		Tunnel string `yaml:"tunnel"`
	}
	_ = yaml.Unmarshal([]byte(rawYaml), &doc)

	res, err := h.cfAPI.ForceReplaceDNSRecord(req.Hostname, doc.Tunnel)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (h *CloudflareHandler) CreateWildcard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	rawYaml, err := h.cfAPI.ActiveConfig(h.configMgr)
	if err != nil {
		http.Error(w, `{"error":"failed to read config"}`, http.StatusInternalServerError)
		return
	}

	var doc struct {
		Tunnel string `yaml:"tunnel"`
	}
	_ = yaml.Unmarshal([]byte(rawYaml), &doc)
	if doc.Tunnel == "" {
		http.Error(w, `{"error":"no tunnel ID found"}`, http.StatusBadRequest)
		return
	}

	domain := strings.TrimSpace(req.Domain)
	if domain == "" {
		http.Error(w, `{"error":"domain name is required"}`, http.StatusBadRequest)
		return
	}

	if err := h.cfAPI.CreateWildcardRoute(domain, doc.Tunnel); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Wildcard CNAME (*." + domain + ") created in Cloudflare DNS pointing to " + doc.Tunnel + ".cfargotunnel.com",
	})
}

func (h *CloudflareHandler) GetTunnelStatus(w http.ResponseWriter, r *http.Request) {
	identity, err := h.configMgr.TunnelIdentity()
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	status, err := h.cfAPI.TunnelStatus(identity)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *CloudflareHandler) GetTunnelConfiguration(w http.ResponseWriter, r *http.Request) {
	identity, err := h.configMgr.TunnelIdentity()
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	configuration, err := h.cfAPI.TunnelConfiguration(identity)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"identity": identity, "config": configuration})
}

func (h *CloudflareHandler) ListAccessApps(w http.ResponseWriter, r *http.Request) {
	apps, err := h.cfAPI.ListAccessApplications()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apps)
}

func (h *CloudflareHandler) CreateAccessApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostname        string `json:"hostname"`
		AuthType        string `json:"auth_type"`
		Emails          string `json:"emails"`
		Domains         string `json:"domains"`
		IPRanges        string `json:"ip_ranges"`
		SessionDuration string `json:"session_duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Hostname == "" {
		http.Error(w, `{"error":"hostname is required"}`, http.StatusBadRequest)
		return
	}

	appID, err := h.cfAPI.SyncZeroTrustAccess(service.ZeroTrustPolicyOptions{
		Hostname:        req.Hostname,
		AuthType:        req.AuthType,
		Emails:          req.Emails,
		Domains:         req.Domains,
		IPRanges:        req.IPRanges,
		SessionDuration: req.SessionDuration,
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"app_id":  appID,
		"message": "Zero Trust Access Application created/updated for " + req.Hostname,
	})
}

func (h *CloudflareHandler) DeleteAccessApp(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, `{"error":"id is required"}`, http.StatusBadRequest)
		return
	}

	if err := h.cfAPI.DeleteZeroTrustAccess(id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Zero Trust Access Application removed",
	})
}
