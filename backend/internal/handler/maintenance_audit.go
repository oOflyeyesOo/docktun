package handler

import (
	"docktun/internal/service"
	"fmt"
	"net/http"
	"strings"
)

// Audit only request identity and outcome. Bodies and query strings may contain
// recovery keys or credentials and must never enter diagnostic/audit exports.
func AuditMaintenanceMutations(audit *service.AuditLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action := maintenanceAuditAction(r.Method, r.URL.Path)
			if action == "" || audit == nil {
				next.ServeHTTP(w, r)
				return
			}
			response := &maintenanceAuditResponse{ResponseWriter: w}
			next.ServeHTTP(response, r)
			status := response.status
			if status == 0 {
				status = http.StatusOK
			}
			user, _ := r.Context().Value("user_id").(int)
			audit.Log(user, action, fmt.Sprintf("%s %s (%d)", r.Method, r.URL.Path, status), r.RemoteAddr)
		})
	}
}
func maintenanceAuditAction(method, path string) string {
	if method != "POST" && method != "PUT" && method != "DELETE" {
		return ""
	}
	switch {
	case strings.HasPrefix(path, "/api/recovery/"):
		return "recovery_action"
	case path == "/api/support-bundles":
		return "support_preview"
	case strings.HasPrefix(path, "/api/operations/") && strings.HasSuffix(path, "/acknowledge"):
		return "operation_acknowledge"
	case strings.HasPrefix(path, "/api/notifications/deliveries/") && strings.HasSuffix(path, "/retry"):
		return "notification_retry"
	case strings.HasPrefix(path, "/api/docker/containers/"):
		if strings.HasSuffix(path, "/readiness") {
			return "application_readiness"
		}
		if strings.HasSuffix(path, "/update-plan") {
			return "application_stage"
		}
		if strings.HasSuffix(path, "/exposure") {
			return "application_exposure"
		}
	}
	return ""
}

type maintenanceAuditResponse struct {
	http.ResponseWriter
	status int
}

func (w *maintenanceAuditResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *maintenanceAuditResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *maintenanceAuditResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
