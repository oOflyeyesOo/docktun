package service

import (
	"database/sql"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"time"
)

type SessionClaims struct {
	UserID   int    `json:"sub"`
	Username string `json:"username"`
	Kind     string `json:"kind"`
	Session  string `json:"sid"`
	jwt.RegisteredClaims
}
type AuthSessions struct {
	Secret string
	DB     *sql.DB
}

func (s AuthSessions) Verify(raw, kind string) (*SessionClaims, error) {
	claims := &SessionClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return []byte(s.Secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || token == nil || !token.Valid || claims.UserID <= 0 || claims.Username == "" || claims.Kind != kind || claims.Session == "" || claims.ID == "" {
		return nil, fmt.Errorf("invalid or expired session")
	}
	var refresh string
	err = s.DB.QueryRow(`SELECT a.refresh_jti FROM auth_sessions a JOIN users u ON u.id=a.user_id WHERE a.id=? AND a.user_id=? AND u.username=? AND a.revoked=0 AND a.expires_at>?`, claims.Session, claims.UserID, claims.Username, time.Now().Unix()).Scan(&refresh)
	if err != nil || (kind == "refresh" && refresh != claims.ID) {
		return nil, fmt.Errorf("invalid or expired session")
	}
	return claims, nil
}
func (s AuthSessions) pair(id int, username, sid, refreshID string) (string, string, error) {
	sign := func(kind, jti string, ttl time.Duration) (string, error) {
		claims := SessionClaims{UserID: id, Username: username, Kind: kind, Session: sid, RegisteredClaims: jwt.RegisteredClaims{ID: jti, ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)), IssuedAt: jwt.NewNumericDate(time.Now())}}
		return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.Secret))
	}
	access, err := sign("access", uuid.NewString(), 24*time.Hour)
	if err != nil {
		return "", "", err
	}
	refresh, err := sign("refresh", refreshID, 30*24*time.Hour)
	return access, refresh, err
}
func (s AuthSessions) Issue(id int, username string) (string, string, error) {
	return s.issue(id, username, "")
}
func (s AuthSessions) IssueVerified(id int, username, hash string) (string, string, error) {
	return s.issue(id, username, hash)
}
func (s AuthSessions) issue(id int, username, hash string) (string, string, error) {
	// Expired sessions need not grow the database indefinitely.
	_, _ = s.DB.Exec(`DELETE FROM auth_sessions WHERE expires_at<=?`, time.Now().Unix())
	sid, rid := uuid.NewString(), uuid.NewString()
	access, refresh, err := s.pair(id, username, sid, rid)
	if err != nil {
		return "", "", err
	}
	if hash == "" {
		_, err = s.DB.Exec(`INSERT INTO auth_sessions(id,user_id,refresh_jti,expires_at) VALUES(?,?,?,?)`, sid, id, rid, time.Now().Add(30*24*time.Hour).Unix())
	} else {
		result, e := s.DB.Exec(`INSERT INTO auth_sessions(id,user_id,refresh_jti,expires_at) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM users WHERE id=? AND password_hash=?)`, sid, id, rid, time.Now().Add(30*24*time.Hour).Unix(), id, hash)
		if e != nil {
			return "", "", e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return "", "", fmt.Errorf("password changed during login; retry")
		}
	}
	return access, refresh, err
}
func (s AuthSessions) Refresh(raw string) (string, string, error) {
	c, err := s.Verify(raw, "refresh")
	if err != nil {
		return "", "", err
	}
	rid := uuid.NewString()
	access, refresh, err := s.pair(c.UserID, c.Username, c.Session, rid)
	if err != nil {
		return "", "", err
	}
	result, err := s.DB.Exec(`UPDATE auth_sessions SET refresh_jti=?, expires_at=? WHERE id=? AND refresh_jti=? AND revoked=0 AND expires_at>?`, rid, time.Now().Add(30*24*time.Hour).Unix(), c.Session, c.ID, time.Now().Unix())
	if err != nil {
		return "", "", err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return "", "", fmt.Errorf("refresh token was already used")
	}
	return access, refresh, nil
}
func (s AuthSessions) Revoke(raw string) error {
	c, err := s.Verify(raw, "access")
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE auth_sessions SET revoked=1 WHERE id=?`, c.Session)
	return err
}
