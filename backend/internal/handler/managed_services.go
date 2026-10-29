package handler

import (
	"context"
	"docktun/internal/service"
	"encoding/json"
	"net/http"
	"time"
)

type ManagedServices struct {
	svc   *service.ManagedServices
	audit *service.AuditLogger
}

func NewManagedServices(svc *service.ManagedServices, audit *service.AuditLogger) *ManagedServices {
	return &ManagedServices{svc: svc, audit: audit}
}
func (h *ManagedServices) Status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	state, err := h.svc.Status(ctx)
	if err != nil {
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(state)
}
func (h *ManagedServices) Check(w http.ResponseWriter, r *http.Request)  { h.start(w, r, "check") }
func (h *ManagedServices) Update(w http.ResponseWriter, r *http.Request) { h.start(w, r, "update") }
func (h *ManagedServices) start(w http.ResponseWriter, r *http.Request, action string) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&req); err != nil {
		jsonError(w, "invalid service selection", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	job, err := h.svc.Start(ctx, action, req.IDs)
	if err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	user, _ := r.Context().Value("user_id").(int)
	h.audit.Log(user, "service_"+action, "DockTun service maintenance requested", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(job)
}
