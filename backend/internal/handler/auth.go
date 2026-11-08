package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/service"
	"golang.org/x/crypto/bcrypt"
)

type Auth struct {
	cfg     *config.Config
	db      *db.DB
	audit   *service.AuditLogger
	initErr error
}

func NewAuth(cfg *config.Config, db *db.DB, audit *service.AuditLogger) *Auth {
	h := &Auth{cfg: cfg, db: db, audit: audit}
	if cfg.AdminPassword != "" {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil {
			h.initErr = err
			return h
		}
		if count == 0 {
			if len(cfg.AdminPassword) < 12 || len(cfg.AdminPassword) > 72 {
				h.initErr = fmt.Errorf("bootstrap password must contain 12–72 bytes")
				return h
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), 12)
			if err != nil {
				h.initErr = err
				return h
			}
			_, h.initErr = db.Exec(`INSERT INTO users(username,password_hash) SELECT ?,? WHERE NOT EXISTS(SELECT 1 FROM users)`, cfg.AdminUsername, string(hash))
		}
	}
	return h
}

func (h *Auth) BootstrapError() error { return h.initErr }

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (h *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	var id int
	var hash string
	err := h.db.QueryRow(`SELECT id, password_hash FROM users WHERE username = ?`, req.Username).Scan(&id, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	access, refresh, err := h.Sessions().IssueVerified(id, req.Username, hash)
	if err != nil {
		jsonError(w, "could not create session", 500)
		return
	}

	h.audit.Log(id, "login", "", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    86400,
	})
}

func (h *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value("user_id").(int)
	if err := h.Sessions().Revoke(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); err != nil {
		jsonError(w, "could not end session", 500)
		return
	}
	h.audit.Log(userID, "logout", "", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *Auth) Sessions() service.AuthSessions {
	return service.AuthSessions{Secret: h.cfg.JWTSecret, DB: h.db.DB}
}

func (h *Auth) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		jsonError(w, "invalid request", 400)
		return
	}
	access, refresh, err := h.Sessions().Refresh(strings.TrimSpace(req.RefreshToken))
	if err != nil {
		jsonError(w, "invalid or expired refresh token", 401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenResponse{AccessToken: access, RefreshToken: refresh, ExpiresIn: 86400})
}
