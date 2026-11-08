package handler

import (
	"context"
	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/service"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaintenanceAuditExcludesSecretsAndRecordsOutcome(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err = database.Exec(`INSERT INTO users(id,username,password_hash) VALUES(42,'audit-owner','not-a-login-hash')`); err != nil {
		t.Fatal(err)
	}
	audit := service.NewAuditLogger(database)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	handler := AuditMaintenanceMutations(audit)(next)
	request := httptest.NewRequest("PUT", "/api/recovery/settings?private=do-not-log-query", strings.NewReader(`{"passphrase":"do-not-log-recovery-key"}`))
	request.Header.Set("Authorization", "Bearer do-not-log-token")
	request = request.WithContext(context.WithValue(request.Context(), "user_id", 42))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var user int
	var action, detail string
	if err = database.QueryRow(`SELECT user_id,action,detail FROM audit_logs ORDER BY id DESC LIMIT 1`).Scan(&user, &action, &detail); err != nil {
		t.Fatal(err)
	}
	if user != 42 || action != "recovery_action" || detail != "PUT /api/recovery/settings (202)" {
		t.Fatal(user, action, detail)
	}
	if strings.Contains(detail, "do-not-log") {
		t.Fatal("secret entered audit trail")
	}
	if response.Code != http.StatusAccepted {
		t.Fatal(response.Code)
	}
}
func TestMaintenanceAuditCoversNewMutationsAndLeavesReadOnlyPreviewsAlone(t *testing.T) {
	for _, path := range []string{"/api/recovery/backups", "/api/recovery/id/restore", "/api/recovery/id/remote-upload", "/api/recovery/id/remote-fetch", "/api/recovery/activate", "/api/support-bundles", "/api/operations/id/acknowledge", "/api/notifications/deliveries/id/retry", "/api/docker/containers/id/readiness", "/api/docker/containers/id/exposure", "/api/docker/containers/id/update-plan"} {
		if maintenanceAuditAction("POST", path) == "" {
			t.Fatal("missing mutation audit", path)
		}
	}
	for _, path := range []string{"/api/recovery/backups", "/api/operations", "/api/docker/containers/id/import-preview"} {
		if maintenanceAuditAction("GET", path) != "" {
			t.Fatal("read-only request recorded as mutation", path)
		}
	}
	if maintenanceAuditAction("POST", "/api/docker/containers/id/exposure-plan") != "" {
		t.Fatal("read-only exposure preview recorded as mutation")
	}
}

func TestRecoveryScheduleActorComesFromAuthentication(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := service.NewRecovery(database, &config.Config{DBPath: filepath.Join(t.TempDir(), "app.db")}, service.NewOperationStore(database))
	handler := &Recovery{Service: svc}
	request := httptest.NewRequest("PUT", "/api/recovery/settings", strings.NewReader(`{"actor_id":9999,"retain_count":3,"retain_days":7,"schedule_hours":0,"selection":{"instance":true,"passphrase":"must-not-be-persisted"}}`))
	request = request.WithContext(context.WithValue(request.Context(), "user_id", 42))
	response := httptest.NewRecorder()
	handler.Settings(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	saved, err := svc.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if saved.ActorID != 42 || saved.Selection.Passphrase != "" {
		t.Fatal("caller forged actor or key persisted")
	}
}
