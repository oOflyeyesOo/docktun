package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/luzyver/tunlify/internal/service"
)

type DockerRun struct {
	svc   *service.DockerRunService
	audit *service.AuditLogger
}

func NewDockerRun(svc *service.DockerRunService, audit *service.AuditLogger) *DockerRun {
	return &DockerRun{svc: svc, audit: audit}
}

type runRequestBody struct {
	Command            string `json:"command"`
	Mode               string `json:"mode"` // "compose" or "direct"
	ConnectCloudflared bool   `json:"connect_cloudflared"`
	CreateProject      bool   `json:"create_project"`
	ProjectName        string `json:"project_name"`
}

func (h *DockerRun) Run(w http.ResponseWriter, r *http.Request) {
	var body runRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Command == "" {
		jsonError(w, "command is required", http.StatusBadRequest)
		return
	}

	mode := body.Mode
	if mode == "" {
		mode = "compose"
	}

	userID, _ := r.Context().Value("user_id").(int)
	ip := r.RemoteAddr

	onDone := func(output string) {
		h.audit.Log(userID, "docker_run", body.Command+"\n"+output, ip)
	}

	job, err := h.svc.StartRun(body.Command, mode, body.ConnectCloudflared, body.CreateProject, body.ProjectName, onDone)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.audit.Log(userID, "docker_run_start", body.Command, ip)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"job_id":       job.ID,
		"status":       "started",
		"container":    job.Container,
		"project_name": job.ProjectName,
	})
}

func (h *DockerRun) Output(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	job, ok := h.svc.GetJob(id)
	if !ok {
		jsonError(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}
