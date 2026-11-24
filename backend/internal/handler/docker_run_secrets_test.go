package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"docktun/internal/db"
	"docktun/internal/service"
)

func TestDockerRunAuditAndOutputExcludeEnvironmentCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Docker executable requires a POSIX shell")
	}
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("INSERT INTO users(id,username,password_hash) VALUES(1,'fixture','unused')"); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "execution-args")
	script := "#!/bin/sh\nif [ \"$1\" = container ]; then echo 'No such container' >&2; exit 1; fi\nprintf '%s\\n' \"$@\" > '" + calls + "'\nprintf '%s\\n' 'PASSWORD=private fixture value'\nprintf 'private ' >&2\nsleep 0.05\nprintf 'fixture value\\n' >&2\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	svc := service.NewDockerRun(database, nil)
	h := NewDockerRun(svc, service.NewAuditLogger(database))
	body := `{"command":"docker run --name audit-fixture --env=PASSWORD=\"private fixture value\" -e FLAG=1 alpine", "mode":"direct"}`
	r := httptest.NewRequest(http.MethodPost, "/api/docker/run", strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), "user_id", 1))
	w := httptest.NewRecorder()
	h.Run(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run rejected: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		JobID int `json:"job_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE action='docker_run'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion audit was not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, ok := svc.GetJob(response.JobID)
	if !ok || !job.Done || job.Status != "completed" {
		t.Fatalf("job did not complete: %#v", job)
	}
	raw, _ := json.Marshal(job)
	if strings.Contains(string(raw), "private fixture value") {
		t.Fatalf("job output or command exposes credentials: %s", raw)
	}
	rows, err := database.Query("SELECT detail FROM audit_logs WHERE action IN ('docker_run_start','docker_run')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "private fixture value") || !strings.Contains(detail, "[redacted]") {
			t.Fatalf("unsafe audit detail: %s", detail)
		}
	}
	actual, err := os.ReadFile(calls)
	if err != nil || !strings.Contains(string(actual), "--env=PASSWORD=private fixture value") {
		t.Fatalf("redaction changed actual execution: %q %v", actual, err)
	}
}
