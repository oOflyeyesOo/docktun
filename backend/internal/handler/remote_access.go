package handler

import (
	"encoding/json"
	"net/http"

	"docktun/internal/service"
)

type RemoteAccess struct {
	svc   *service.RemoteAccessService
	audit *service.AuditLogger
}

func NewRemoteAccess(svc *service.RemoteAccessService, audit *service.AuditLogger) *RemoteAccess {
	return &RemoteAccess{svc: svc, audit: audit}
}

func (h *RemoteAccess) Overview(w http.ResponseWriter, r *http.Request) {
	overview, err := h.svc.GetOverview()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(overview)
}

func (h *RemoteAccess) TailscaleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.svc.GetTailscaleStatus()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *RemoteAccess) TailscaleUp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuthKey  string `json:"auth_key"`
		Hostname string `json:"hostname"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := h.svc.TailscaleUp(req.AuthKey, req.Hostname); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	if h.audit != nil {
		h.audit.Log(userID, "tailscale_up", "Connected node to Tailscale tailnet", r.RemoteAddr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *RemoteAccess) TailscaleDown(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.TailscaleDown(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	if h.audit != nil {
		h.audit.Log(userID, "tailscale_down", "Disconnected node from Tailscale tailnet", r.RemoteAddr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *RemoteAccess) TailscaleControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		jsonError(w, "action is required", http.StatusBadRequest)
		return
	}

	if err := h.svc.TailscaleControl(req.Action); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	if h.audit != nil {
		h.audit.Log(userID, "tailscale_control", "Executed Tailscale action: "+req.Action, r.RemoteAddr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *RemoteAccess) WireguardStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.svc.GetWireguardStatus()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *RemoteAccess) WireguardControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		jsonError(w, "action is required", http.StatusBadRequest)
		return
	}

	if err := h.svc.WireguardControl(req.Action); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	if h.audit != nil {
		h.audit.Log(userID, "wireguard_control", "Executed WireGuard action: "+req.Action, r.RemoteAddr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *RemoteAccess) ZeroconfControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		jsonError(w, "action is required", http.StatusBadRequest)
		return
	}

	if err := h.svc.ZeroconfControl(req.Action); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	if h.audit != nil {
		h.audit.Log(userID, "zeroconf_control", "Executed ZeroConf action: "+req.Action, r.RemoteAddr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
