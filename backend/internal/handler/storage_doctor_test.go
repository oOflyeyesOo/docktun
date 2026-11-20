package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/service"
)

func TestStorageDoctorHandler_GetDoctor(t *testing.T) {
	tempDir := t.TempDir()
	dbDir := filepath.Join(tempDir, "data")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "app.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	cfgDir := filepath.Join(tempDir, "cloudflared")
	_ = os.MkdirAll(cfgDir, 0755)
	cfgPath := filepath.Join(cfgDir, "config.yml")
	_ = os.WriteFile(cfgPath, []byte("tunnel: test\n"), 0644)

	cfg := &config.Config{
		DBPath:            dbPath,
		CloudflaredConfig: cfgPath,
	}

	storageSvc := service.NewStorageService(database, cfg)
	storageFilesSvc := service.NewStorageFilesService(database, cfg)
	auditSvc := service.NewAuditLogger(database)

	h := NewStorageHandler(storageSvc, storageFilesSvc, auditSvc)

	req := httptest.NewRequest("GET", "/api/storage/doctor", nil)
	w := httptest.NewRecorder()

	h.GetDoctor(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
	}

	var report service.StorageDoctorReport
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("failed to decode json report: %v", err)
	}

	if report.ChecksRun == 0 {
		t.Fatalf("expected checks to have run, got 0")
	}
}
