package handler

import (
	"docktun/internal/db"
	"docktun/internal/service"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfUpdateCheckReturns200WithDiagnosticPayload(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	audit := service.NewAuditLogger(database)

	updater := service.NewSelfUpdater(t.TempDir())
	updater.SetCheckFailureForTest(errors.New("dial unix /var/run/docker.sock: connect: permission denied"))

	h := NewSelfUpdate(updater, audit)

	req := httptest.NewRequest("POST", "/api/self-update/check", nil)
	w := httptest.NewRecorder()
	h.Check(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 for recoverable diagnostic check error, got: %d (%s)", w.Code, w.Body.String())
	}

	var state service.SelfUpdateState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	if state.Phase != "failed" {
		t.Errorf("expected state.Phase to be 'failed', got: %s", state.Phase)
	}
	if !state.Checked {
		t.Errorf("expected state.Checked to be true")
	}
	if !strings.Contains(state.Error, "Docker socket permission denied") {
		t.Errorf("expected diagnostic error message, got: %s", state.Error)
	}
}
