package handler

import (
	"context"
	"docktun/internal/service"
	"encoding/json"
	"net/http"
	"time"
)

type SelfUpdate struct {
	svc   *service.SelfUpdater
	audit *service.AuditLogger
}

func NewSelfUpdate(svc *service.SelfUpdater, audit *service.AuditLogger) *SelfUpdate {
	return &SelfUpdate{svc, audit}
}
func (h *SelfUpdate) Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.svc.Status())
}
func (h *SelfUpdate) Check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	state, err := h.svc.Check(ctx)
	if err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}
func (h *SelfUpdate) Install(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	state, err := h.svc.Start(ctx)
	if err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	user, _ := r.Context().Value("user_id").(int)
	h.audit.Log(user, "self_update", state.Message, r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(state)
}
