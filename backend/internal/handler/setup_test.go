package handler

import (
	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/middleware"
	"docktun/internal/service"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupRoutes(t *testing.T) {
	dir := t.TempDir()
	database, err := db.New(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := &config.Config{JWTSecret: "test-secret", SetupToken: "0123456789abcdef0123456789abcdef"}
	auth := NewAuth(cfg, database, nil)
	h := NewSetup(service.NewSetupService(cfg, database, nil, nil, nil, nil, nil, dir), auth)
	r := chi.NewRouter()
	r.Get("/api/setup/status", h.Status)
	r.Post("/api/setup/complete", h.Complete)
	r.Group(func(r chi.Router) {
		r.Use(middleware.NewAuth(cfg.JWTSecret).Verify)
		r.Get("/api/setup/details", h.Details)
		r.Post("/api/setup/reconfigure", h.Reconfigure)
	})
	for _, path := range []string{"/api/setup/details", "/api/setup/reconfigure"} {
		w := httptest.NewRecorder()
		method := "GET"
		if strings.Contains(path, "reconfigure") {
			method = "POST"
		}
		r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/setup/complete", strings.NewReader(`{"setup_token":"0123456789abcdef0123456789abcdef","admin_username":"owner","admin_password":"strong-password"}`)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result service.SetupCompleteResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/setup/status?force=true", nil))
	var status map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &status)
	if len(status) != 2 || status["completed"] != true {
		t.Fatal(status)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/setup/complete?force=true", strings.NewReader(`{"admin_username":"owner","admin_password":"attacker-password"}`)))
	if w.Code != http.StatusConflict {
		t.Fatal(w.Code)
	}
	req := httptest.NewRequest("POST", "/api/setup/reconfigure", strings.NewReader(`{"admin_username":"owner"}`))
	req.Header.Set("Authorization", "Bearer "+result.AccessToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var updated service.SetupCompleteResult
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.AccessToken != "" || updated.RefreshToken != "" {
		t.Fatal("reconfiguration minted tokens")
	}
}
