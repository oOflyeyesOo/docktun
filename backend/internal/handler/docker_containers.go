package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/luzyver/tunlify/internal/service"
)

type DockerContainers struct {
	svc *service.DockerContainers
}

func NewDockerContainers(svc *service.DockerContainers) *DockerContainers {
	return &DockerContainers{svc: svc}
}

func (h *DockerContainers) List(w http.ResponseWriter, r *http.Request) {
	containers, err := h.svc.List()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(containers)
}

func (h *DockerContainers) Details(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	details, err := h.svc.Details(id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

func (h *DockerContainers) Exposure(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	exp, err := h.svc.GetExposure(id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(exp)
}

func (h *DockerContainers) UpdateExposure(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req service.ExposureSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.svc.UpdateExposure(id, req); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *DockerContainers) Start(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Start(id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": "start"})
}

func (h *DockerContainers) Stop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Stop(id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": "stop"})
}

func (h *DockerContainers) Restart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Restart(id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": "restart"})
}

func (h *DockerContainers) Remove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && r.Body != http.NoBody {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.svc.Remove(id, req.Force); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": "remove"})
}

func (h *DockerContainers) Logs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 {
		tail = 100
	}

	logs, err := h.svc.Logs(id, tail)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"logs": logs})
}
