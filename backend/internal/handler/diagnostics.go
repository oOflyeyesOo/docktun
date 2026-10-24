package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type diagnosticCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type diagnosticRunner func(context.Context, ...string) ([]byte, error)

// Diagnostics deliberately returns fixed failure messages: CLI output can contain
// host paths, registry credentials, or daemon connection details.
func Diagnostics() http.HandlerFunc {
	return diagnostics(func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "docker", args...).Output()
	})
}

func diagnostics(run diagnosticRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		checks := make([]diagnosticCheck, 0, 2)
		for _, probe := range []struct {
			name    string
			args    []string
			failure string
		}{
			{"Docker engine", []string{"version", "--format", "{{.Server.Version}}"}, "Cannot reach Docker. Check that the engine is running and the Docker socket is mounted with access."},
			{"Docker Compose", []string{"compose", "version", "--short"}, "Compose is unavailable. Project actions require the Docker Compose plugin in the DockTun image."},
		} {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			out, err := run(ctx, probe.args...)
			cancel()
			check := diagnosticCheck{Name: probe.name, OK: err == nil && strings.TrimSpace(string(out)) != "", Message: probe.failure}
			if check.OK {
				check.Message = "Available"
			}
			checks = append(checks, check)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(struct {
			CheckedAt time.Time         `json:"checked_at"`
			Checks    []diagnosticCheck `json:"checks"`
		}{time.Now().UTC(), checks})
	}
}
