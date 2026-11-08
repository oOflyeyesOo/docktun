package handler

import (
	"bytes"
	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/service"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestLogoutRevokesRefreshToken(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	audit := service.NewAuditLogger(database)
	cfg := &config.Config{JWTSecret: "test-secret"}
	auth := NewAuth(cfg, database, audit)
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO users (username, password_hash) VALUES (?, ?)`, "owner", string(hash)); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	auth.Login(login, httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"owner","password":"password"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", login.Code, login.Body.String())
	}
	var tokens tokenResponse
	if err := json.Unmarshal(login.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logoutRequest.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	logout := httptest.NewRecorder()
	auth.Logout(logout, logoutRequest)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status %d", logout.Code)
	}

	refresh := httptest.NewRecorder()
	refreshRequest := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewBufferString(`{"refresh_token":"`+tokens.RefreshToken+`"}`))
	auth.Refresh(refresh, refreshRequest)
	if refresh.Code != http.StatusUnauthorized {
		t.Fatalf("revoked refresh status %d: %s", refresh.Code, refresh.Body.String())
	}
}
