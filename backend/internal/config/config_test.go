package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentSecrets(t *testing.T) {
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "app.db"))
	t.Setenv("JWT_SECRET", "")
	t.Setenv("SETUP_TOKEN", "")
	a, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.JWTSecret) != 64 || a.JWTSecret != b.JWTSecret || len(a.SetupToken) != 64 || a.SetupToken != b.SetupToken {
		t.Fatal("secrets not persistent")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(a.DBPath), "jwt.secret"), []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("weak secret accepted")
	}
}
