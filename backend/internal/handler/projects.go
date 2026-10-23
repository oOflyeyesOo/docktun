package handler

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"docktun/internal/service"
)

type Projects struct {
	svc   *service.Projects
	audit *service.AuditLogger
}

func NewProjects(svc *service.Projects, audit *service.AuditLogger) *Projects {
	return &Projects{svc: svc, audit: audit}
}

func (h *Projects) List(w http.ResponseWriter, r *http.Request) {
	projects, err := h.svc.List()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []service.Project{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}

func (h *Projects) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           string `json:"name"`
		Path           string `json:"path"`
		RepoURL        string `json:"repo_url"`
		GitUsername    string `json:"git_username"`
		GitToken       string `json:"git_token"`
		ComposeContent string `json:"compose_content"`
		EnvContent     string `json:"env_content"`
		AutoStart      bool   `json:"auto_start"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		jsonError(w, "project name required", http.StatusBadRequest)
		return
	}

	body.Name = strings.TrimSpace(body.Name)
	if strings.TrimSpace(body.Path) == "" {
		body.Path = filepath.Join("./projects", body.Name)
	}

	p, err := h.svc.Create(body.Name, body.Path, body.RepoURL, body.GitUsername, body.GitToken)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If direct compose content provided, save docker-compose.yml
	if strings.TrimSpace(body.ComposeContent) != "" {
		_ = h.svc.SaveComposeFile(p.ID, "docker-compose.yml", body.ComposeContent)
	}

	// If direct env content provided, save .env
	if strings.TrimSpace(body.EnvContent) != "" {
		_ = h.svc.SaveEnvFile(p.ID, body.EnvContent)
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "project_create", body.Name, r.RemoteAddr)

	// If auto_start is true and compose file exists, start compose up -d asynchronously
	if body.AutoStart && strings.TrimSpace(body.ComposeContent) != "" {
		onDone := func(output string) {
			h.audit.Log(userID, "project_up", p.Name+"\n"+output, r.RemoteAddr)
		}
		h.svc.ComposeAction(p.ID, p.Path, onDone, "up", "-d")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

func (h *Projects) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var body struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		RepoURL     string `json:"repo_url"`
		GitUsername string `json:"git_username"`
		GitToken    string `json:"git_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Path == "" {
		jsonError(w, "name and path required", http.StatusBadRequest)
		return
	}

	if err := h.svc.Update(id, body.Name, body.Path, body.RepoURL, body.GitUsername, body.GitToken); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "project_update", body.Name, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Projects) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if err := h.svc.Delete(id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "project_delete", chi.URLParam(r, "id"), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Projects) Prune(w http.ResponseWriter, r *http.Request) {
	count, err := h.svc.PruneOrphans()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "projects_prune", fmt.Sprintf("Pruned %d orphan/duplicate projects", count), r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "pruned": count})
}

func (h *Projects) History(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	p, err := h.svc.GetByID(id)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}
	logs, err := h.audit.ByDetail(p.Name, 20)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (h *Projects) Output(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	lines, done := h.svc.GetOutput(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"lines": lines, "done": done})
}

func (h *Projects) Action(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	action := chi.URLParam(r, "action")

	p, err := h.svc.GetByID(id)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}

	var reqBody struct {
		Ref string `json:"ref"`
	}
	json.NewDecoder(r.Body).Decode(&reqBody)

	if action == "deploy" && reqBody.Ref == "" && p.RepoURL != "" {
		jsonError(w, "ref (branch/tag) required", http.StatusBadRequest)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)
	ip := r.RemoteAddr

	onDone := func(output string) {
		detail := p.Name
		if action == "deploy" {
			detail = p.Name + "\n" + output
		}
		h.audit.Log(userID, "project_"+action, detail, ip)
	}

	switch action {
	case "up":
		h.svc.ComposeAction(id, p.Path, onDone, "up", "-d")
	case "down":
		h.svc.ComposeAction(id, p.Path, onDone, "down")
	case "restart":
		h.svc.ComposeAction(id, p.Path, onDone, "restart")
	case "deploy":
		h.svc.DeployStream(id, p, reqBody.Ref, onDone)
	default:
		jsonError(w, "invalid action", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Projects) ComposeGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	filename, content, err := h.svc.GetComposeFile(id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"filename": filename,
		"content":  content,
	})
}

func (h *Projects) ComposeSave(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	p, err := h.svc.GetByID(id)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}
	var body struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := h.svc.SaveComposeFile(id, body.Filename, body.Content); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "project_compose_save", p.Name, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Projects) EnvGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	content, err := h.svc.GetEnvFile(id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"content": content})
}

func (h *Projects) EnvSave(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	p, err := h.svc.GetByID(id)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := h.svc.SaveEnvFile(id, body.Content); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "project_env_save", p.Name, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Projects) FilesList(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	relPath := r.URL.Query().Get("path")
	files, err := h.svc.ListFiles(id, relPath)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (h *Projects) FileGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		jsonError(w, "path parameter required", http.StatusBadRequest)
		return
	}
	content, err := h.svc.GetFile(id, relPath)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"path":    relPath,
		"content": content,
	})
}

func (h *Projects) FileSave(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	p, err := h.svc.GetByID(id)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		jsonError(w, "path and content required", http.StatusBadRequest)
		return
	}
	if err := h.svc.SaveFile(id, body.Path, body.Content); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "project_file_save", p.Name+": "+body.Path, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Projects) FileCreate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	p, err := h.svc.GetByID(id)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		IsDir   bool   `json:"is_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		jsonError(w, "path required", http.StatusBadRequest)
		return
	}
	if err := h.svc.CreateFile(id, body.Path, body.IsDir, body.Content); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	userID, _ := r.Context().Value("user_id").(int)
	action := "project_file_create"
	if body.IsDir {
		action = "project_dir_create"
	}
	h.audit.Log(userID, action, p.Name+": "+body.Path, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Projects) FileDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	p, err := h.svc.GetByID(id)
	if err != nil {
		jsonError(w, "project not found", http.StatusNotFound)
		return
	}
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		jsonError(w, "path parameter required", http.StatusBadRequest)
		return
	}
	if err := h.svc.DeleteFile(id, relPath); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	userID, _ := r.Context().Value("user_id").(int)
	h.audit.Log(userID, "project_file_delete", p.Name+": "+relPath, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
