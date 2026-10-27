package handler

import (
	"encoding/json"
	"net/http"

	"docktun/internal/service"
	"github.com/go-chi/chi/v5"
)

type Control struct {
	cfd   *service.Cloudflared
	audit *service.AuditLogger
}

func NewControl(cfd *service.Cloudflared, audit *service.AuditLogger) *Control {
	return &Control{cfd: cfd, audit: audit}
}

func (h *Control) Connector(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var req struct {
			Token string `json:"token"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 70*1024)
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			jsonError(w, "invalid connector credentials", http.StatusBadRequest)
			return
		}
		if err := h.cfd.SaveConnectorToken(req.Token); err != nil {
			jsonError(w, "unable to save connector credentials", http.StatusInternalServerError)
			return
		}
		userID, _ := r.Context().Value("user_id").(int)
		if h.audit != nil {
			h.audit.Log(userID, "cloudflare_connector_credentials", "Connector credentials updated; running state unchanged", r.RemoteAddr)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	identity, err := h.cfd.TunnelIdentity()
	response := map[string]any{"has_token": h.cfd.HasConnectorToken(), "tunnel_id": identity.ID, "account_id": identity.AccountID, "remote_managed": identity.Remote}
	if err != nil && identity.Remote {
		response["identity_error"] = err.Error()
	}
	json.NewEncoder(w).Encode(response)
}

func (h *Control) Execute(w http.ResponseWriter, r *http.Request) {
	action := chi.URLParam(r, "action")
	userID, _ := r.Context().Value("user_id").(int)

	var err error
	switch action {
	case "restart":
		err = h.cfd.Restart()
	case "start":
		err = h.cfd.Start()
	case "stop":
		err = h.cfd.Stop()
	default:
		http.Error(w, `{"error":"invalid action"}`, http.StatusBadRequest)
		return
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	h.audit.Log(userID, action, "", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": action})
}
