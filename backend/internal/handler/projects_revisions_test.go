package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"docktun/internal/db"
	"docktun/internal/service"
	"github.com/go-chi/chi/v5"
)

func TestProjectsHandler_RevisionsAndRollback(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "app.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	projDir := filepath.Join(tempDir, "my-stack")
	_ = os.MkdirAll(projDir, 0755)

	res, err := database.Exec(`INSERT INTO projects (name, path) VALUES ('my-stack', ?)`, projDir)
	if err != nil {
		t.Fatal(err)
	}
	pID, _ := res.LastInsertId()
	projectID := int(pID)

	projectsSvc := service.NewProjects(database, tempDir)
	auditSvc := service.NewAuditLogger(database)
	h := NewProjects(projectsSvc, auditSvc)

	// 1. Initial save via service
	if err := projectsSvc.SaveComposeFile(projectID, "docker-compose.yml", "services:\n  app:\n    image: nginx:1.24\n"); err != nil {
		t.Fatal(err)
	}
	// 2. Second save
	if err := projectsSvc.SaveComposeFile(projectID, "docker-compose.yml", "services:\n  app:\n    image: nginx:1.25\n"); err != nil {
		t.Fatal(err)
	}

	// Test GET /api/projects/{id}/revisions
	r := chi.NewRouter()
	r.Get("/api/projects/{id}/revisions", h.GetRevisions)
	r.Post("/api/projects/{id}/revisions/{revID}/rollback", h.RollbackRevision)

	req := httptest.NewRequest("GET", "/api/projects/1/revisions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
	}

	var revisions []service.ComposeRevision
	if err := json.NewDecoder(w.Body).Decode(&revisions); err != nil {
		t.Fatalf("failed to decode revisions: %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(revisions))
	}

	firstRevID := revisions[1].ID

	// Test POST /api/projects/{id}/revisions/{revID}/rollback
	reqRollback := httptest.NewRequest("POST", "/api/projects/1/revisions/1/rollback", nil)
	ctx := context.WithValue(reqRollback.Context(), "user_id", 1)
	reqRollback = reqRollback.WithContext(ctx)
	wRollback := httptest.NewRecorder()
	r.ServeHTTP(wRollback, reqRollback)

	if wRollback.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 on rollback, got %d: %s", wRollback.Code, wRollback.Body.String())
	}

	var rollbackRes map[string]interface{}
	if err := json.NewDecoder(wRollback.Body).Decode(&rollbackRes); err != nil {
		t.Fatalf("failed to decode rollback response: %v", err)
	}
	if rollbackRes["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", rollbackRes["status"])
	}

	// Verify disk compose file has nginx:1.24
	_, content, err := projectsSvc.GetComposeFile(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "nginx:1.24") {
		t.Fatalf("expected rolled back compose to contain nginx:1.24, got: %s", content)
	}

	_ = firstRevID
}
