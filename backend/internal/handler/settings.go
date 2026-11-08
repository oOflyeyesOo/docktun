package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"docktun/internal/db"
	"docktun/internal/service"
	"golang.org/x/crypto/bcrypt"
)

type Settings struct {
	db    *db.DB
	audit *service.AuditLogger
}

func NewSettings(db *db.DB, audit *service.AuditLogger) *Settings {
	return &Settings{db: db, audit: audit}
}

func (h *Settings) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	userID, _ := r.Context().Value("user_id").(int)

	var hash string
	h.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash)

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)) != nil {
		http.Error(w, `{"error":"current password incorrect"}`, http.StatusUnauthorized)
		return
	}

	if len(req.NewPassword) < 12 || len(req.NewPassword) > 72 {
		jsonError(w, "password must contain 12–72 bytes", 400)
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		jsonError(w, "could not hash password", 500)
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		jsonError(w, "could not save password", 500)
		return
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE users SET password_hash=? WHERE id=? AND password_hash=?`, string(newHash), userID, hash)
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if n != 1 {
			err = fmt.Errorf("password changed; reload and retry")
		}
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE auth_sessions SET revoked=1 WHERE user_id=?`, userID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		jsonError(w, "could not save password; reload and retry", 500)
		return
	}

	h.audit.Log(userID, "password_change", "", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
