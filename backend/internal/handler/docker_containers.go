package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"docktun/internal/service"
	"github.com/go-chi/chi/v5"
)

type DockerContainers struct {
	svc *service.DockerContainers
}

func NewDockerContainers(svc *service.DockerContainers) *DockerContainers {
	return &DockerContainers{svc: svc}
}

func (h *DockerContainers) TailscaleNode(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("user_id").(int)
	status, err := h.svc.ContainerTailscale(chi.URLParam(r, "id"), r.Method == http.MethodPost, userID, r.RemoteAddr)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
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

func (h *DockerContainers) Batch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 || req.Action == "" {
		jsonError(w, "ids and action are required", http.StatusBadRequest)
		return
	}

	results := h.svc.BatchAction(req.IDs, req.Action)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
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
	timestamps := r.URL.Query().Get("timestamps") == "true"
	if tail <= 0 {
		tail = 100
	}

	logs, err := h.svc.Logs(id, tail, timestamps)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"logs": logs})
}

func (h *DockerContainers) ConnectNetwork(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Network string `json:"network"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := h.svc.ConnectNetwork(id, req.Network); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": "connect_network"})
}

func (h *DockerContainers) DisconnectNetwork(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Network string `json:"network"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := h.svc.DisconnectNetwork(id, req.Network); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": "disconnect_network"})
}

func (h *DockerContainers) EnsureProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	projID, err := h.svc.EnsureProjectForContainer(id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "project_id": projID})
}

func (h *DockerContainers) Exec(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		jsonError(w, "command is required", http.StatusBadRequest)
		return
	}

	out, exitCode, err := h.svc.Exec(id, req.Command)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"output":    out,
		"exit_code": exitCode,
	})
}

func (h *DockerContainers) SystemDF(w http.ResponseWriter, r *http.Request) {
	df, err := h.svc.SystemDF()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(df)
}

func (h *DockerContainers) SystemPrune(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.SystemPrune()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"output": out,
	})
}

func (h *DockerContainers) ProbeTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service       string                     `json:"service"`
		OriginRequest service.OriginProbeOptions `json:"origin_request"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Service == "" {
		jsonError(w, "service is required", http.StatusBadRequest)
		return
	}

	res, err := h.svc.ProbeTargetWithOptions(req.Service, req.OriginRequest)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (h *DockerContainers) CheckUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	res, err := h.svc.CheckContainerUpdate(id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (h *DockerContainers) CheckAllUpdates(w http.ResponseWriter, r *http.Request) {
	results, err := h.svc.CheckAllUpdates()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (h *DockerContainers) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, _ := r.Context().Value("user_id").(int)
	ip := r.RemoteAddr

	cname := id
	if detail, err := h.svc.Details(id); err == nil && detail.Name != "" {
		cname = strings.TrimPrefix(detail.Name, "/")
	}

	var input struct {
		ExpectedImage string `json:"expected_image_id"`
	}
	if r.ContentLength > 0 && json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
		jsonError(w, "invalid update request", 400)
		return
	}
	out, err := h.svc.UpdateContainerPinned(id, userID, ip, input.ExpectedImage)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  err.Error(),
			"output": out,
		})
		return
	}

	newID := id
	if freshDetail, err := h.svc.Details(cname); err == nil && freshDetail.ID != "" {
		newID = freshDetail.ID
		if freshDetail.Name != "" {
			cname = strings.TrimPrefix(freshDetail.Name, "/")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"output":         out,
		"container_id":   newID,
		"container_name": cname,
	})
}

func (h *DockerContainers) ReconcileNetworks(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.ReconcileNetworks()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (h *DockerContainers) RelinkDatabase(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.EnsureDatabaseLinks(id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "database links and network aliases successfully verified",
	})
}

func (h *DockerContainers) ExposurePreflight(w http.ResponseWriter, r *http.Request) {
	var input service.ExposureSettings
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input) != nil {
		jsonError(w, "invalid exposure settings", 400)
		return
	}
	plan, err := h.svc.ExposurePreflight(r.Context(), chi.URLParam(r, "id"), input)
	if err != nil {
		jsonError(w, "container could not be inspected", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plan)
}
