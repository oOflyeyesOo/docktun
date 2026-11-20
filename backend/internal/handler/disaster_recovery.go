package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"docktun/internal/service"
)

type DisasterRecoveryHandler struct {
	svc   *service.DisasterRecoveryService
	audit *service.AuditLogger
}

func NewDisasterRecovery(svc *service.DisasterRecoveryService, audit *service.AuditLogger) *DisasterRecoveryHandler {
	return &DisasterRecoveryHandler{svc: svc, audit: audit}
}

// Export streams a consolidated disaster recovery archive bundle as a gzip attachment.
func (h *DisasterRecoveryHandler) Export(w http.ResponseWriter, r *http.Request) {
	filename := fmt.Sprintf("docktun-backup-%s.tar.gz", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	if err := h.svc.ExportBackup(w); err != nil {
		// If error occurs, header was set but body might fail.
		return
	}

	if h.audit != nil {
		userID, _ := r.Context().Value("user_id").(int)
		h.audit.Log(userID, "system_backup_export", fmt.Sprintf("Exported disaster recovery archive %s", filename), r.RemoteAddr)
	}
}

// Restore receives a multipart archive upload, validates structure, and restores components.
func (h *DisasterRecoveryHandler) Restore(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		jsonError(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("backup")
	if err != nil {
		file, _, err = r.FormFile("file")
	}
	if err != nil {
		jsonError(w, "missing backup file in request (expected 'backup' or 'file' field)", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if err := h.svc.RestoreBackup(file); err != nil {
		jsonError(w, "restore failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	if h.audit != nil {
		userID, _ := r.Context().Value("user_id").(int)
		h.audit.Log(userID, "system_backup_restore", "Restored disaster recovery archive", r.RemoteAddr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Disaster recovery archive restored successfully",
	})
}
