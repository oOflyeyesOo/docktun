package handler

import (
	"context"
	"docktun/internal/service"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"time"
)

type Maintenance struct {
	Containers    *service.DockerContainers
	Operations    *service.OperationStore
	Notifications *Notifications
}

func (h *Maintenance) Plan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	plan, err := h.Containers.UpdatePlan(ctx, chi.URLParam(r, "id"), r.Method == "POST")
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plan)
}
func (h *Maintenance) Readiness(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
		jsonError(w, "invalid readiness setting", 400)
		return
	}
	if err := h.Containers.SaveReadiness(chi.URLParam(r, "id"), input.URL); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
func (h *Maintenance) ImportPreview(w http.ResponseWriter, r *http.Request) {
	preview, err := h.Containers.ImportPreview(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(preview)
}
func (h *Maintenance) History(w http.ResponseWriter, r *http.Request) {
	items, err := h.Operations.List(r.URL.Query().Get("target"))
	if err != nil {
		jsonError(w, "could not read operation history", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}
func (h *Maintenance) Acknowledge(w http.ResponseWriter, r *http.Request) {
	if err := h.Operations.Acknowledge(chi.URLParam(r, "id")); err != nil {
		jsonError(w, err.Error(), 409)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
func (h *Maintenance) Deliveries(w http.ResponseWriter, r *http.Request) {
	items, err := h.Notifications.queue.List(false, time.Now().Unix(), r.URL.Query().Get("target"))
	if err != nil {
		jsonError(w, "could not read delivery history", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}
func (h *Maintenance) RetryDelivery(w http.ResponseWriter, r *http.Request) {
	if err := h.Notifications.queue.Retry(chi.URLParam(r, "id")); err != nil {
		jsonError(w, err.Error(), 409)
		return
	}
	select {
	case h.Notifications.wake <- struct{}{}:
	default:
	}
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
