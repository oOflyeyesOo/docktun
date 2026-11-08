package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
		ok   bool
	}{
		{"available", "29.0\n", nil, true},
		{"failed", "secret-host-path", errors.New("secret-error"), false},
		{"empty", "\n", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := diagnostics(func(ctx context.Context, args ...string) ([]byte, error) {
				calls++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
					t.Fatal("missing bounded deadline")
				}
				if calls == 1 && strings.Join(args, " ") != "version --format {{.Server.Version}}" {
					t.Fatal(args)
				}
				if calls == 2 && strings.Join(args, " ") != "compose version --short" {
					t.Fatal(args)
				}
				return []byte(tc.out), tc.err
			})
			w := httptest.NewRecorder()
			h(w, httptest.NewRequest("GET", "/api/diagnostics", nil))
			var result struct {
				Checks []diagnosticCheck `json:"checks"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if calls != 2 || len(result.Checks) != 2 {
				t.Fatal(result)
			}
			for _, check := range result.Checks {
				if check.OK != tc.ok {
					t.Fatal(check)
				}
			}
			if strings.Contains(w.Body.String(), "secret") {
				t.Fatal("CLI output leaked")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("diagnostics cached")
			}
		})
	}
}
