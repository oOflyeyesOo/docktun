package handler

import (
	"context"
	"docktun/internal/service"
	"encoding/json"
	"net/http"
	"time"
)

type SelfUpdate struct {
	svc        *service.SelfUpdater
	audit      *service.AuditLogger
	operations *service.OperationStore
}

func NewSelfUpdate(svc *service.SelfUpdater, audit *service.AuditLogger) *SelfUpdate {
	return &SelfUpdate{svc: svc, audit: audit}
}
func (h *SelfUpdate) WithOperations(store *service.OperationStore) *SelfUpdate {
	h.operations = store
	return h
}

func (h *SelfUpdate) Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
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
	state, err := h.svc.StartJournaled(ctx, h.operations)
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

func (h *SelfUpdate) Channel(w http.ResponseWriter, r *http.Request) {
	if r.Method == "PUT" {
		var input struct {
			Channel string `json:"channel"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
			jsonError(w, "invalid channel", 400)
			return
		}
		if h.operations != nil {
			var count int
			if h.operations.DB.QueryRow(`SELECT count(*) FROM operation_locks`).Scan(&count) != nil || count > 0 {
				jsonError(w, "finish active operations before changing channels", 409)
				return
			}
		}
		if err := h.svc.SaveChannel(input.Channel); err != nil {
			jsonError(w, err.Error(), 409)
			return
		}
		user, _ := r.Context().Value("user_id").(int)
		h.audit.Log(user, "update_channel", input.Channel, r.RemoteAddr)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.svc.Channel())
}
