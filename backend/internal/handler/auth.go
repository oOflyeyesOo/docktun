package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"docktun/internal/config"
	"docktun/internal/db"
	"docktun/internal/service"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type Auth struct {
	cfg   *config.Config
	db    *db.DB
	audit *service.AuditLogger
}

func NewAuth(cfg *config.Config, db *db.DB, audit *service.AuditLogger) *Auth {
	if cfg.AdminPassword != "" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), 12)
		db.Exec(`
			INSERT INTO users (username, password_hash) VALUES (?, ?)
			ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash
		`, cfg.AdminUsername, string(hash))
	}
	return &Auth{cfg: cfg, db: db, audit: audit}
}

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

	access, _ := h.generateToken(id, req.Username, 24*time.Hour, "access")
	refresh, _ := h.generateToken(id, req.Username, 30*24*time.Hour, "refresh")

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
	h.revokeBearerToken(r)
	h.audit.Log(userID, "logout", "", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *Auth) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil && r.ContentLength > 0 {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	tokenStr := strings.TrimSpace(req.RefreshToken)
	if tokenStr == "" {
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	var userID int
	var username string

	if tokenStr != "" {
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			return []byte(h.cfg.JWTSecret), nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err == nil && token.Valid && !h.isRevoked(token) {
			if claims, ok := token.Claims.(jwt.MapClaims); ok {
				if sub, ok := claims["sub"].(float64); ok && sub > 0 {
					userID = int(sub)
				}
				if u, ok := claims["username"].(string); ok {
					username = u
				}
			}
		}
	}

	if userID == 0 || username == "" {
		if uid, ok := r.Context().Value("user_id").(int); ok && uid > 0 {
			userID = uid
		}
		if uname, ok := r.Context().Value("username").(string); ok && uname != "" {
			username = uname
		}
	}

	if userID == 0 || username == "" {
		http.Error(w, `{"error":"invalid or expired refresh token"}`, http.StatusUnauthorized)
		return
	}

	if tokenStr != "" {
		h.revokeTokenString(tokenStr)
	}
	access, _ := h.generateToken(userID, username, 24*time.Hour, "access")
	refresh, _ := h.generateToken(userID, username, 30*24*time.Hour, "refresh")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    86400,
	})
}

func (h *Auth) GenerateToken(userID int, username string, duration time.Duration) (string, error) {
	return h.generateToken(userID, username, duration, "")
}

func (h *Auth) generateToken(userID int, username string, duration time.Duration, kind string) (string, error) {
	claims := jwt.MapClaims{
		"sub":      userID,
		"username": username,
		"exp":      time.Now().Add(duration).Unix(),
		"iat":      time.Now().Unix(),
		"jti":      uuid.NewString(),
	}
	if kind != "" {
		claims["kind"] = kind
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.cfg.JWTSecret))
}

func (h *Auth) revokeBearerToken(r *http.Request) {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		h.revokeTokenString(strings.TrimPrefix(header, "Bearer "))
	}
}

func (h *Auth) revokeTokenString(tokenString string) {
	_, _ = h.db.Exec(`DELETE FROM token_revocations WHERE expires_at <= CURRENT_TIMESTAMP`)
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		return []byte(h.cfg.JWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || token == nil {
		return
	}
	if jti, ok := token.Claims.(jwt.MapClaims)["jti"].(string); ok && jti != "" {
		if exp, ok := token.Claims.(jwt.MapClaims)["exp"].(float64); ok {
			_, _ = h.db.Exec(`INSERT OR REPLACE INTO token_revocations (jti, expires_at) VALUES (?, datetime(?, 'unixepoch'))`, jti, int64(exp))
		}
	}
}

func (h *Auth) isRevoked(token *jwt.Token) bool {
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return false
	}
	jti, ok := claims["jti"].(string)
	if !ok || jti == "" {
		return false
	}
	var exists int
	err := h.db.QueryRow(`SELECT 1 FROM token_revocations WHERE jti=? AND expires_at > CURRENT_TIMESTAMP`, jti).Scan(&exists)
	return err == nil
}
