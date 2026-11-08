package handler

import (
	"docktun/internal/service"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"time"
)

type Recovery struct{ Service *service.RecoveryService }

func (h *Recovery) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.List()
	if err != nil {
		jsonError(w, "could not read recovery history", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(items)
}
func (h *Recovery) Backup(w http.ResponseWriter, r *http.Request) {
	var input service.RecoveryRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input) != nil {
		jsonError(w, "invalid backup selection", 400)
		return
	}
	item, err := h.Service.StartBackup(input)
	input.Passphrase = ""
	if err != nil {
		jsonError(w, err.Error(), 409)
		return
	}
	user, _ := r.Context().Value("user_id").(int)
	_, _ = h.Service.DB.Exec(`INSERT INTO audit_logs(user_id,action,detail,ip_address) VALUES(?,'recovery_backup',?,?)`, user, item.ID, r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	json.NewEncoder(w).Encode(item)
}
func (h *Recovery) Download(w http.ResponseWriter, r *http.Request) {
	file, err := h.Service.Download(chi.URLParam(r, "id"))
	if err != nil {
		jsonError(w, "recovery archive unavailable", 404)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="docktun-recovery.dtrec"`)
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, file)
}
func (h *Recovery) Upload(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, (16<<30)+(32<<20))
	item, err := h.Service.Import(r.Body)
	if err != nil {
		jsonError(w, "encrypted recovery archive import failed; check format, size and disk capacity", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(item)
}
func (h *Recovery) Inspect(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	var input service.RecoveryRestoreRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input) != nil {
		jsonError(w, "invalid recovery plan", 400)
		return
	}
	plan, err := h.Service.PlanRestore(r.Context(), chi.URLParam(r, "id"), input)
	input.Passphrase = ""
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(plan)
}
func (h *Recovery) Restore(w http.ResponseWriter, r *http.Request) {
	var input service.RecoveryRestoreRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input) != nil {
		jsonError(w, "invalid restore request", 400)
		return
	}
	job, err := h.Service.StartRestore(chi.URLParam(r, "id"), input)
	input.Passphrase = ""
	if err != nil {
		jsonError(w, err.Error(), 409)
		return
	}
	user, _ := r.Context().Value("user_id").(int)
	_, _ = h.Service.DB.Exec(`INSERT INTO audit_logs(user_id,action,detail,ip_address) VALUES(?,'recovery_restore',?,?)`, user, job.ID, r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	json.NewEncoder(w).Encode(job)
}
func (h *Recovery) Status(w http.ResponseWriter, r *http.Request) {
	required := h.Service.Config.RecoveryMode
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"review_required": required, "note": "Recovered instances do not prepare providers or reconcile public routes until reviewed and restarted. Review project paths, provider identities and notification destinations."})
}
func (h *Recovery) Activate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirmed bool `json:"confirmed"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || !input.Confirmed {
		jsonError(w, "confirm that recovered host paths, identities and notification destinations were reviewed", 400)
		return
	}
	if _, err := h.Service.DB.Exec(`DELETE FROM system_settings WHERE key='recovery_review_required'`); err != nil {
		jsonError(w, "could not save recovery review", 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"restart_required": true, "note": "Restart DockTun after removing any explicit DOCKTUN_RECOVERY_MODE environment override to enable normal reconciliation."})
}

func (h *Recovery) Settings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "PUT" {
		var input service.RecoverySettings
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input) != nil {
			jsonError(w, "invalid recovery settings", 400)
			return
		}
		if err := h.Service.SaveSettings(input); err != nil {
			jsonError(w, err.Error(), 400)
			return
		}
	}
	settings, err := h.Service.Settings()
	if err != nil {
		jsonError(w, "could not read recovery settings", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}
func (h *Recovery) UploadRemote(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	if err := h.Service.UploadRemote(r.Context(), chi.URLParam(r, "id")); err != nil {
		jsonError(w, err.Error(), 502)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"verified": true})
}
func (h *Recovery) FetchRemote(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	item, err := h.Service.FetchRemote(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		jsonError(w, err.Error(), 502)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(item)
}
func (h *Recovery) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.DeleteLocal(chi.URLParam(r, "id")); err != nil {
		jsonError(w, err.Error(), 409)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"removed": true})
}

func (h *Recovery) Ticket(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user, _ := r.Context().Value("user_id").(int)
	ticket, err := h.Service.DownloadTicket(id, user)
	if err != nil {
		jsonError(w, "archive is unavailable", 404)
		return
	}
	path := "/downloads/recovery/" + id
	http.SetCookie(w, &http.Cookie{Name: "docktun_recovery_download", Value: ticket, Path: path, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 60})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"url": path})
}
func (h *Recovery) NativeDownload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cookie, err := r.Cookie("docktun_recovery_download")
	if err != nil || h.Service.ConsumeDownload(id, cookie.Value) != nil {
		http.Error(w, "Generate a new archive download from the signed-in dashboard", 401)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "docktun_recovery_download", Value: "", Path: "/downloads/recovery/" + id, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	file, err := h.Service.Download(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Disposition", `attachment; filename="docktun-recovery.dtrec"`)
	http.ServeContent(w, r, "docktun-recovery.dtrec", info.ModTime(), file)
}

func (h *Recovery) Inventory(w http.ResponseWriter, r *http.Request) {
	data, err := h.Service.Inventory(r.Context())
	if err != nil {
		jsonError(w, "Recovery inventory unavailable; check Docker access", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
