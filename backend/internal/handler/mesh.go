package handler

import (
	"encoding/json"
	"net/http"

	"docktun/internal/service"
)

type Mesh struct {
	svc *service.MeshService
}

func NewMesh(svc *service.MeshService) *Mesh {
	return &Mesh{svc: svc}
}

func (h *Mesh) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.Load()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func (h *Mesh) Save(w http.ResponseWriter, r *http.Request) {
	var req service.MeshConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.svc.Save(&req); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
