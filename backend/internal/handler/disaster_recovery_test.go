package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/service"
)

func TestDisasterRecoveryHandler_ExportAndRestore(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "app.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer database.Close()

	cfDir := filepath.Join(tempDir, "cloudflared")
	os.MkdirAll(cfDir, 0755)
	os.WriteFile(filepath.Join(cfDir, "config.yml"), []byte("tunnel: test\n"), 0644)

	projDir := filepath.Join(tempDir, "projects")
	os.MkdirAll(filepath.Join(projDir, "demo"), 0755)
	os.WriteFile(filepath.Join(projDir, "demo", "docker-compose.yml"), []byte("services: {}\n"), 0644)

	cfg := &config.Config{DBPath: dbPath, CloudflaredConfig: filepath.Join(cfDir, "config.yml")}
	drSvc := service.NewDisasterRecoveryService(database, cfg).WithPaths(dbPath, cfDir, projDir)
	auditSvc := service.NewAuditLogger(database)
	h := NewDisasterRecovery(drSvc, auditSvc)

	// 1. Test Export Endpoint
	reqExport := httptest.NewRequest(http.MethodGet, "/api/system/backup/export", nil)
	recExport := httptest.NewRecorder()
	h.Export(recExport, reqExport)

	if recExport.Code != http.StatusOK {
		t.Fatalf("expected status 200 on export, got %d", recExport.Code)
	}
	contentType := recExport.Header().Get("Content-Type")
	if contentType != "application/gzip" {
		t.Fatalf("expected application/gzip, got %s", contentType)
	}
	disposition := recExport.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment; filename=\"docktun-backup-") {
		t.Fatalf("expected Content-Disposition attachment, got %s", disposition)
	}
	exportBytes := recExport.Body.Bytes()
	if len(exportBytes) == 0 {
		t.Fatal("expected non-empty export payload")
	}

	// 2. Test Restore Endpoint with exported archive
	targetDir := t.TempDir()
	targetDB := filepath.Join(targetDir, "app.db")
	targetCF := filepath.Join(targetDir, "cloudflared")
	targetProj := filepath.Join(targetDir, "projects")
	restoreDrSvc := service.NewDisasterRecoveryService(nil, nil).WithPaths(targetDB, targetCF, targetProj)
	hRestore := NewDisasterRecovery(restoreDrSvc, auditSvc)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("backup", "docktun-backup.tar.gz")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(exportBytes); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	writer.Close()

	reqRestore := httptest.NewRequest(http.MethodPost, "/api/system/backup/restore", &body)
	reqRestore.Header.Set("Content-Type", writer.FormDataContentType())
	recRestore := httptest.NewRecorder()

	hRestore.Restore(recRestore, reqRestore)

	if recRestore.Code != http.StatusOK {
		t.Fatalf("expected status 200 on restore, got %d, body: %s", recRestore.Code, recRestore.Body.String())
	}
	if !strings.Contains(recRestore.Body.String(), `"status":"ok"`) {
		t.Fatalf("expected status ok in response, got %s", recRestore.Body.String())
	}

	// Verify restored files exist
	if _, err := os.Stat(targetDB); err != nil {
		t.Fatalf("restored db not found: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(targetCF, "config.yml")); err != nil {
		t.Fatalf("restored cf config not found: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(targetProj, "demo", "docker-compose.yml")); err != nil {
		t.Fatalf("restored compose file not found: %v", err)
	}

	// 3. Test Restore with missing file form
	reqBad := httptest.NewRequest(http.MethodPost, "/api/system/backup/restore", bytes.NewReader([]byte("not a multipart")))
	reqBad.Header.Set("Content-Type", "text/plain")
	recBad := httptest.NewRecorder()
	hRestore.Restore(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 on bad request, got %d", recBad.Code)
	}
}
