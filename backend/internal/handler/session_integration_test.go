package handler

import (
	"bytes"
	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/middleware"
	"docktun/internal/service"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestBrowserSessionLifecycle(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := &config.Config{JWTSecret: "session-test-secret", AdminUsername: "owner", AdminPassword: "initial-password-123"}
	audit := service.NewAuditLogger(database)
	auth := NewAuth(cfg, database, audit)
	mw := middleware.NewAuth(cfg.JWTSecret).WithValidator(func(raw string) (int, string, error) {
		c, e := auth.Sessions().Verify(raw, "access")
		if e != nil {
			return 0, "", e
		}
		return c.UserID, c.Username, nil
	})
	r := chi.NewRouter()
	r.Post("/auth/login", auth.Login)
	r.Post("/auth/refresh", auth.Refresh)
	r.Group(func(r chi.Router) {
		r.Use(mw.Verify)
		r.Post("/auth/logout", auth.Logout)
		r.Post("/password", NewSettings(database, audit).ChangePassword)
		r.Get("/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	})
	request := func(path string, body any, token string, want int) tokenResponse {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		if path == "/private" {
			req.Method = "GET"
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var result tokenResponse
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	login := func() tokenResponse {
		return request("/auth/login", map[string]string{"username": "owner", "password": "initial-password-123"}, "", 200)
	}
	tokens := login()
	request("/private", nil, tokens.AccessToken, 200)
	request("/private", nil, tokens.RefreshToken, 401)
	rotated := request("/auth/refresh", map[string]string{"refresh_token": tokens.RefreshToken}, "", 200)
	request("/auth/refresh", map[string]string{"refresh_token": tokens.RefreshToken}, "", 401)
	request("/auth/logout", nil, rotated.AccessToken, 200)
	request("/private", nil, tokens.AccessToken, 401)
	request("/auth/refresh", map[string]string{"refresh_token": rotated.RefreshToken}, "", 401)
	tokens = login()
	request("/password", map[string]string{"current_password": "initial-password-123", "new_password": string(bytes.Repeat([]byte("x"), 73))}, tokens.AccessToken, 400)
	login()
	request("/password", map[string]string{"current_password": "initial-password-123", "new_password": "replacement-password-123"}, tokens.AccessToken, 200)
	request("/private", nil, tokens.AccessToken, 401)
	request("/auth/refresh", map[string]string{"refresh_token": tokens.RefreshToken}, "", 401)
	// Restart with the original environment password must not overwrite Settings.
	NewAuth(cfg, database, audit)
	request("/auth/login", map[string]string{"username": "owner", "password": "initial-password-123"}, "", 401)
	request("/auth/login", map[string]string{"username": "owner", "password": "replacement-password-123"}, "", 200)
}
