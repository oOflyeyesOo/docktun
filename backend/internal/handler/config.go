package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"

	"docktun/internal/service"
	"github.com/go-chi/chi/v5"
)

type Config struct {
	mgr   *service.ConfigManager
	audit *service.AuditLogger
	cfAPI *service.CloudflareAPI
}

func NewConfig(mgr *service.ConfigManager, audit *service.AuditLogger, cfAPI *service.CloudflareAPI) *Config {
	return &Config{mgr: mgr, audit: audit, cfAPI: cfAPI}
}

func (h *Config) Get(w http.ResponseWriter, r *http.Request) {
	content, err := h.mgr.Read()
	if os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"content": "", "configured": false})
		return
	}
	if err != nil {
		http.Error(w, `{"error":"failed to read config"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"content": content})
}

func (h *Config) Update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if err := h.mgr.Validate(req.Content); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "validation failed: " + err.Error()})
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	if err := h.mgr.Write(req.Content, userID); err != nil {
		http.Error(w, `{"error":"failed to write config"}`, http.StatusInternalServerError)
		return
	}

	h.audit.Log(userID, "config_edit", "", r.RemoteAddr)

	// Automatically sync all ingress routes with Cloudflare DNS
	if h.cfAPI != nil {
		go h.cfAPI.AutoSyncIngressDNS(h.mgr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Config) Validate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Content string `json:"content"`
	}
	json.Unmarshal(body, &req)

	if err := h.mgr.Validate(req.Content); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"valid": "false", "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"valid": "true"})
}

func (h *Config) ListBackups(w http.ResponseWriter, r *http.Request) {
	limit, offset := 50, 0
	if l := r.URL.Query().Get("limit"); l != "" {
		limit, _ = strconv.Atoi(l)
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		offset, _ = strconv.Atoi(o)
	}

	backups, err := h.mgr.ListBackups(limit, offset)
	if err != nil {
		http.Error(w, `{"error":"failed to list backups"}`, http.StatusInternalServerError)
		return
	}
	if backups == nil {
		backups = []service.ConfigBackup{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backups)
}

func (h *Config) CreateBackup(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("user_id").(int)
	if err := h.mgr.CreateBackup("", userID); err != nil {
		http.Error(w, `{"error":"failed to create backup: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	h.audit.Log(userID, "config_backup_create", "manual backup", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Config) GetBackup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	backup, err := h.mgr.GetBackup(id)
	if err != nil {
		http.Error(w, `{"error":"backup not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backup)
}

func (h *Config) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	if err := h.mgr.DeleteBackup(id); err != nil {
		http.Error(w, `{"error":"failed to delete backup"}`, http.StatusInternalServerError)
		return
	}
	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "config_backup_delete", strconv.Itoa(id), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
