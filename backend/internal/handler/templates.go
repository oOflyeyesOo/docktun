package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"docktun/internal/service"
)

type Templates struct{ library *service.TemplateLibrary }

func NewTemplates() *Templates { return &Templates{library: service.NewTemplateLibrary()} }
func (h *Templates) Catalog(w http.ResponseWriter, r *http.Request) {
	items, err := h.library.Catalog(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}
func (h *Templates) Import(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		jsonError(w, "invalid public Compose URL", http.StatusBadRequest)
		return
	}
	content, source, err := h.library.Import(r.Context(), body.URL)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"compose_content": content, "source_url": source})
}
func (h *Templates) Review(w http.ResponseWriter, r *http.Request) {
	var body service.ComposeReviewInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body) != nil {
		jsonError(w, "invalid Compose review request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	result, err := service.ReviewCompose(ctx, body)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
