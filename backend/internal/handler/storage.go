package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"docktun/internal/service"
)

type StorageHandler struct {
	svc     *service.StorageService
	fileSvc *service.StorageFilesService
	audit   *service.AuditLogger
}

func NewStorageHandler(svc *service.StorageService, fileSvc *service.StorageFilesService, audit *service.AuditLogger) *StorageHandler {
	return &StorageHandler{
		svc:     svc,
		fileSvc: fileSvc,
		audit:   audit,
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

// -----------------------------------------------------------------------------
// File Manager Handlers
// -----------------------------------------------------------------------------

func (h *StorageHandler) ListTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := h.fileSvc.ListTargets()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if targets == nil {
		targets = []service.StorageTarget{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(targets)
}

func (h *StorageHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	targetType := r.URL.Query().Get("target_type")
	targetID := r.URL.Query().Get("target_id")
	path := r.URL.Query().Get("path")

	if targetType == "" || targetID == "" {
		jsonError(w, "target_type and target_id required", http.StatusBadRequest)
		return
	}

	files, err := h.fileSvc.ListFiles(targetType, targetID, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if files == nil {
		files = []service.FileNode{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (h *StorageHandler) ReadFile(w http.ResponseWriter, r *http.Request) {
	targetType := r.URL.Query().Get("target_type")
	targetID := r.URL.Query().Get("target_id")
	path := r.URL.Query().Get("path")

	if targetType == "" || targetID == "" || path == "" {
		jsonError(w, "target_type, target_id, and path required", http.StatusBadRequest)
		return
	}

	result, err := h.fileSvc.ReadFile(targetType, targetID, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *StorageHandler) WriteFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Path       string `json:"path"`
		Content    string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if body.TargetType == "" || body.TargetID == "" || body.Path == "" {
		jsonError(w, "target_type, target_id, and path required", http.StatusBadRequest)
		return
	}

	if err := h.fileSvc.WriteFile(body.TargetType, body.TargetID, body.Path, body.Content); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_file_edit", fmt.Sprintf("Edited file in %s/%s: %s", body.TargetType, body.TargetID, body.Path), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *StorageHandler) CreateItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Path       string `json:"path"`
		IsDir      bool   `json:"is_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if body.TargetType == "" || body.TargetID == "" || body.Path == "" {
		jsonError(w, "target_type, target_id, and path required", http.StatusBadRequest)
		return
	}

	if err := h.fileSvc.CreateItem(body.TargetType, body.TargetID, body.Path, body.IsDir); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	itemType := "file"
	if body.IsDir {
		itemType = "directory"
	}
	h.audit.Log(userID, "storage_file_create", fmt.Sprintf("Created %s in %s/%s: %s", itemType, body.TargetType, body.TargetID, body.Path), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *StorageHandler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	targetType := r.URL.Query().Get("target_type")
	targetID := r.URL.Query().Get("target_id")
	path := r.URL.Query().Get("path")

	if targetType == "" || targetID == "" || path == "" {
		jsonError(w, "target_type, target_id, and path required", http.StatusBadRequest)
		return
	}

	if err := h.fileSvc.DeleteItem(targetType, targetID, path); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_file_delete", fmt.Sprintf("Deleted in %s/%s: %s", targetType, targetID, path), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *StorageHandler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	targetType := r.URL.Query().Get("target_type")
	targetID := r.URL.Query().Get("target_id")
	path := r.URL.Query().Get("path")

	if targetType == "" || targetID == "" || path == "" {
		jsonError(w, "target_type, target_id, and path required", http.StatusBadRequest)
		return
	}

	stream, filename, size, err := h.fileSvc.GetDownloadStream(targetType, targetID, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}

	io.Copy(w, stream)
}

func (h *StorageHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form up to 64MB
	if err := r.ParseMultipartForm(64 * 1024 * 1024); err != nil {
		jsonError(w, "file too large or invalid multipart form", http.StatusBadRequest)
		return
	}

	targetType := r.FormValue("target_type")
	targetID := r.FormValue("target_id")
	targetDir := r.FormValue("dir_path")

	if targetType == "" || targetID == "" {
		jsonError(w, "target_type and target_id required", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "missing file in upload form", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if err := h.fileSvc.UploadFile(targetType, targetID, targetDir, header.Filename, file); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "storage_file_upload", fmt.Sprintf("Uploaded %s to %s/%s/%s", header.Filename, targetType, targetID, targetDir), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":   "ok",
		"filename": header.Filename,
	})
}
