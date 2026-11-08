package handler

import (
	"context"
	"docktun/internal/service"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"time"
)

type Support struct{ Service *service.SupportBundles }

func (h *Support) Create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IncludeOperations bool `json:"include_operations"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
		jsonError(w, "invalid support bundle request", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	preview, err := h.Service.Create(ctx, input.IncludeOperations)
	if err != nil {
		jsonError(w, "could not generate support preview", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(preview)
}
func (h *Support) Download(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Service.Read(chi.URLParam(r, "id"))
	if err != nil {
		jsonError(w, "support bundle unavailable or expired", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="docktun-support.json"`)
	w.Write(raw)
}
