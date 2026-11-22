package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"docktun/internal/db"
	"docktun/internal/service"
	"github.com/go-chi/chi/v5"
)

func TestProjectActionsExposeIsolatedOutputAndRejectOverlap(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	result, err := database.Exec("INSERT INTO projects(name,path) VALUES(?,?)", "operation-fixture", filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := result.LastInsertId()
	if projectID != 1 {
		t.Fatalf("unexpected fixture ID %d", projectID)
	}
	projects := service.NewProjects(database, "")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	projects.SetRequireMountInspectionForTest(func() bool {
		once.Do(func() { close(entered) })
		<-release
		return false
	})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	handler := NewProjects(projects, service.NewAuditLogger(database))
	router := chi.NewRouter()
	router.Post("/api/projects/{id}/action/{action}", handler.Action)
	router.Get("/api/projects/{id}/output", handler.Output)
	router.Delete("/api/projects/{id}", handler.Delete)
	request := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader("{}")))
		return recorder
	}
	first := request(http.MethodPost, "/api/projects/1/action/up")
	if first.Code != http.StatusOK {
		t.Fatalf("first action failed: %d %s", first.Code, first.Body.String())
	}
	var action struct {
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &action); err != nil || action.OperationID == "" {
		t.Fatalf("missing operation identity: %s %v", first.Body.String(), err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not begin")
	}
	for _, path := range []string{"/api/projects/1/action/down", "/api/projects/1/action/deploy"} {
		blocked := request(http.MethodPost, path)
		if blocked.Code != http.StatusConflict {
			t.Fatalf("overlap %s returned %d: %s", path, blocked.Code, blocked.Body.String())
		}
	}
	blocked := request(http.MethodDelete, "/api/projects/1")
	if blocked.Code != http.StatusConflict {
		t.Fatalf("delete overlapped active action: %d %s", blocked.Code, blocked.Body.String())
	}
	output := request(http.MethodGet, "/api/projects/1/output?operation_id="+action.OperationID)
	if output.Code != http.StatusOK || !strings.Contains(output.Body.String(), `"done":false`) || output.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("active output unavailable: %d %s", output.Code, output.Body.String())
	}
	for _, path := range []string{"/api/projects/2/output?operation_id=" + action.OperationID, "/api/projects/1/output?operation_id=expired"} {
		missing := request(http.MethodGet, path)
		if missing.Code != http.StatusNotFound {
			t.Fatalf("invalid output identity accepted: %s %d", path, missing.Code)
		}
	}
	unblock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		output = request(http.MethodGet, "/api/projects/1/output?operation_id="+action.OperationID)
		if strings.Contains(output.Body.String(), `"done":true`) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker did not complete")
}

func TestProjectCreateReportsSavedProjectWhenAutoStartIsBusy(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	projects := service.NewProjects(database, "")
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var reserveOnce sync.Once
	var existingOperation string
	projects.SetRequireMountInspectionForTest(func() bool {
		reserveOnce.Do(func() {
			existingOperation, err = projects.ComposeAction(1, "", func(string) { <-release }, "up")
			if err != nil {
				t.Errorf("concurrent start: %v", err)
			}
		})
		return false
	})
	handler := NewProjects(projects, service.NewAuditLogger(database))
	router := chi.NewRouter()
	router.Post("/api/projects", handler.Create)
	body, err := json.Marshal(map[string]any{
		"name": "create-busy", "path": t.TempDir(),
		"compose_content": "services:\n  app:\n    image: alpine:3.23\n",
		"auto_start":      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(string(body))))
	if response.Code != http.StatusCreated {
		t.Fatalf("saved project reported failure: %d %s", response.Code, response.Body.String())
	}
	var created struct {
		ID             int    `json:"id"`
		OperationID    string `json:"operation_id"`
		OperationError string `json:"operation_error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != 1 || created.OperationID != "" || created.OperationError == "" {
		t.Fatalf("missing partial-start result: %s", response.Body.String())
	}
	if _, err := projects.GetByID(created.ID); err != nil {
		t.Fatal("saved project lost", err)
	}
	unblock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, done, err := projects.GetOperationOutput(created.ID, existingOperation)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("concurrent fixture operation did not finish")
}
