package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"docktun/internal/service"
)

type StorageHandler struct {
	svc   *service.StorageService
	audit *service.AuditLogger
}

func NewStorageHandler(svc *service.StorageService, audit *service.AuditLogger) *StorageHandler {
	return &StorageHandler{
		svc:   svc,
		audit: audit,
	}
}

func (h *StorageHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.GetOverview()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *StorageHandler) ListVolumes(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.ListVolumes()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = []service.VolumeItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *StorageHandler) DeleteVolume(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if strings.TrimSpace(name) == "" {
		jsonError(w, "volume name required", http.StatusBadRequest)
		return
	}

	out, err := h.svc.DeleteVolume(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_delete_volume", fmt.Sprintf("Deleted volume %s", name), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"output": out,
	})
}

func (h *StorageHandler) PruneVolumes(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.PruneVolumes()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_prune_volumes", fmt.Sprintf("Pruned unused volumes:\n%s", out), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"output": out,
	})
}

func (h *StorageHandler) ListImages(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.ListImages()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = []service.ImageItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *StorageHandler) DeleteImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		jsonError(w, "image ID required", http.StatusBadRequest)
		return
	}

	force := r.URL.Query().Get("force") == "true"
	out, err := h.svc.DeleteImage(id, force)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_delete_image", fmt.Sprintf("Deleted image %s (force=%v)", id, force), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"output": out,
	})
}

func (h *StorageHandler) PruneImages(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"
	out, err := h.svc.PruneImages(all)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	actionDesc := "dangling"
	if all {
		actionDesc = "all unused"
	}
	h.audit.Log(userID, "storage_prune_images", fmt.Sprintf("Pruned %s images:\n%s", actionDesc, out), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"output": out,
	})
}

func (h *StorageHandler) ListContainers(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.ListContainersStorage()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = []service.ContainerStorageItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *StorageHandler) PruneContainers(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.PruneContainers()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_prune_containers", fmt.Sprintf("Pruned stopped containers:\n%s", out), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"output": out,
	})
}

func (h *StorageHandler) PruneBuildCache(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"
	out, err := h.svc.PruneBuildCache(all)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_prune_build_cache", fmt.Sprintf("Pruned build cache (all=%v):\n%s", all, out), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"output": out,
	})
}

func (h *StorageHandler) ReclaimBatch(w http.ResponseWriter, r *http.Request) {
	var req service.ReclaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	res, err := h.svc.ReclaimBatch(req)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_batch_reclaim", fmt.Sprintf("Smart Reclaim executed:\n%s", strings.Join(res.Outputs, "\n\n")), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (h *StorageHandler) VacuumDB(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.VacuumDB()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_vacuum_db", fmt.Sprintf("%v", res["message"]), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
