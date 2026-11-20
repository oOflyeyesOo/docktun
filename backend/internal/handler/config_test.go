package handler

import (
	"docktun/internal/config"
	"docktun/internal/service"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUnconfiguredCloudflareIsNotReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	handler := NewConfig(service.NewConfigManager(&config.Config{CloudflaredConfig: path}, nil), nil, nil)
	response := httptest.NewRecorder()
	handler.Get(response, httptest.NewRequest("GET", "/api/config", nil))
	var body map[string]any
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || body["content"] != "" || body["configured"] != false {
		t.Fatal(response.Code, body)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.Get(response, httptest.NewRequest("GET", "/api/config", nil))
	if response.Code != 500 {
		t.Fatal("hid a real read failure", response.Code)
	}
}

func TestReconcileNetworksEndpointWithoutContainersService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	h := NewConfig(service.NewConfigManager(&config.Config{CloudflaredConfig: path}, nil), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/cloudflare/reconcile-networks", nil)
	h.ReconcileNetworks(rec, req)
	if rec.Code != 500 {
		t.Fatalf("expected 500 when containers service is nil, got %d", rec.Code)
	}
}
