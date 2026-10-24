package handler

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthAddress(t *testing.T) {
	for _, tc := range []struct{ target, want string }{
		{"http://service", "service:80"}, {"https://service", "service:443"},
		{"http://[::1]", "[::1]:80"}, {"https://[::1]:8443", "[::1]:8443"},
		{"tcp://service:1234", "service:1234"}, {"ssh://service:22", "service:22"},
		{"rdp://service:3389", "service:3389"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			got, err := healthAddress(tc.target)
			if err != nil || got != tc.want {
				t.Fatalf("%q %v", got, err)
			}
		})
	}
	for _, target := range []string{"", "service", "file:///etc/passwd", "ftp://service", "tcp://service", "http://service:0", "http://service:65536", "http://service:bad", "http://user:pass@service"} {
		if _, err := healthAddress(target); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
}

func TestHealthCheck(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	path := filepath.Join(t.TempDir(), "config.yml")
	h := NewHealthCheck(path)
	for _, tc := range []struct {
		name, yaml string
		code       int
	}{
		{"malformed", "ingress: [", 500},
		{"valid", "ingress:\n  - hostname: example.test\n    service: http://" + listener.Addr().String() + "\n  - service: http_status:404\n", 200},
		{"empty", "ingress: []", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.Check(w, httptest.NewRequest("GET", "/api/health", nil))
			if w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.name == "valid" {
				var results []serviceHealth
				if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
					t.Fatal(err)
				}
				if len(results) != 1 || results[0].Status != "up" {
					t.Fatal(results)
				}
			}
		})
	}
}
