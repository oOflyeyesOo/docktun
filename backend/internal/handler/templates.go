package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"docktun/internal/service"
)

type Templates struct{ library *service.TemplateLibrary }

func NewTemplates() *Templates { return &Templates{library: service.NewTemplateLibrary()} }

func (h *Templates) Setup(w http.ResponseWriter, r *http.Request) {
	var input service.TemplateSetupInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&input) != nil {
		jsonError(w, "invalid template setup request", http.StatusBadRequest)
		return
	}
	result, err := service.ConfigureTemplate(input)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(result)
}
func (h *Templates) Ports(w http.ResponseWriter, r *http.Request) {
	var input service.TemplateSetupInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&input) != nil {
		jsonError(w, "invalid port request", 400)
		return
	}
	result, err := service.FindTemplatePorts(r.Context(), input)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(result)
}
func (h *Templates) Files(w http.ResponseWriter, r *http.Request) {
	var input service.TemplateFilesInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&input) != nil {
		jsonError(w, "invalid file request", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	result, err := h.library.TemplateFiles(ctx, input)
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(result)
}
func (h *Templates) Catalog(w http.ResponseWriter, r *http.Request) {
	var items []service.CommunityTemplate
	var err error
	if source := r.URL.Query().Get("url"); source != "" {
		items, err = h.library.PortainerCatalog(r.Context(), source)
	} else {
		items, err = h.library.Catalog(r.Context())
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}
func (h *Templates) Import(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL        string `json:"url"`
		Collection string `json:"collection"`
		ID         string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		jsonError(w, "invalid public Compose URL", http.StatusBadRequest)
		return
	}
	var content, source string
	var err error
	if body.Collection != "" {
		content, source, err = h.library.ImportPortainer(r.Context(), body.Collection, body.ID)
	} else {
		content, source, err = h.library.Import(r.Context(), body.URL)
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	projectSource := source
	if body.Collection != "" {
		projectSource = ""
		if first, _, ok := strings.Cut(content, "\n"); ok && strings.HasPrefix(first, "# Source: https://raw.githubusercontent.com/") {
			projectSource = strings.TrimPrefix(first, "# Source: ")
		}
	}
	json.NewEncoder(w).Encode(map[string]string{"compose_content": content, "source_url": source, "project_source": projectSource})
}
func (h *Templates) Review(w http.ResponseWriter, r *http.Request) {
	var body service.ComposeReviewInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&body) != nil {
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
