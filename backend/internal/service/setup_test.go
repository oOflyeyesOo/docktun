package service

import (
	"docktun/internal/config"
	"docktun/internal/db"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func setupFixture(t *testing.T) *SetupService {
	t.Helper()
	dir := t.TempDir()
	database, err := db.New(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return NewSetupService(&config.Config{SetupToken: "0123456789abcdef0123456789abcdef"}, database, nil, nil, nil, nil, nil, dir)
}
func TestSetupLifecycle(t *testing.T) {
	s := setupFixture(t)
	for _, password := range []string{"", "short", string(make([]byte, 73))} {
		if _, err := s.Complete(SetupCompleteRequest{SetupToken: "0123456789abcdef0123456789abcdef", AdminPassword: password}, "", nil); !errors.Is(err, ErrSetupInput) {
			t.Fatalf("invalid password: %v", err)
		}
	}
	if _, err := s.Complete(SetupCompleteRequest{AdminPassword: "strong-password"}, "", nil); !errors.Is(err, ErrSetupToken) {
		t.Fatalf("missing ownership token: %v", err)
	}
	calls := 0
	req := SetupCompleteRequest{SetupToken: "0123456789abcdef0123456789abcdef", AdminUsername: "owner", AdminPassword: "strong-password"}
	result, err := s.Complete(req, "", func(id int, name string) (string, string, error) {
		calls++
		if id <= 0 || name != "owner" {
			t.Fatal("invalid identity")
		}
		return "access", "refresh", nil
	})
	if err != nil || !result.OK || calls != 1 {
		t.Fatalf("bootstrap: %+v %v", result, err)
	}
	for _, name := range []string{"owner", "attacker"} {
		req.AdminUsername = name
		if _, err := s.Complete(req, "", nil); !errors.Is(err, ErrSetupLocked) {
			t.Fatalf("takeover: %v", err)
		}
	}
	var id int
	var hash string
	if err := s.db.QueryRow(`SELECT id,password_hash FROM users WHERE username='owner'`).Scan(&id, &hash); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("strong-password")) != nil {
		t.Fatal("password changed")
	}
	if _, err := s.Reconfigure(SetupCompleteRequest{SetupToken: "0123456789abcdef0123456789abcdef", AdminPassword: "replacement-password"}, id, ""); !errors.Is(err, ErrSetupIdentity) {
		t.Fatal(err)
	}
	if _, err := s.Reconfigure(SetupCompleteRequest{}, 999, ""); !errors.Is(err, ErrSetupIdentity) {
		t.Fatal(err)
	}
	result, err = s.Reconfigure(SetupCompleteRequest{}, id, "")
	if err != nil || !result.OK || result.AccessToken != "" || result.RefreshToken != "" {
		t.Fatalf("reconfigure: %+v %v", result, err)
	}
}
func TestConcurrentBootstrap(t *testing.T) {
	s := setupFixture(t)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Complete(SetupCompleteRequest{SetupToken: "0123456789abcdef0123456789abcdef", AdminUsername: fmt.Sprint("owner", i), AdminPassword: "strong-password"}, "", nil)
			if err == nil {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if wins.Load() != 1 || count != 1 {
		t.Fatalf("wins=%d accounts=%d", wins.Load(), count)
	}
}
func TestSetupFailsClosed(t *testing.T) {
	s := setupFixture(t)
	s.db.Exec(`INSERT INTO system_settings(key,value) VALUES('setup_completed','true')`)
	if _, err := s.Complete(SetupCompleteRequest{SetupToken: "0123456789abcdef0123456789abcdef", AdminPassword: "strong-password"}, "", nil); !errors.Is(err, ErrSetupLocked) {
		t.Fatal(err)
	}
	s.db.Close()
	if _, err := s.Locked(); err == nil {
		t.Fatal("database failure ignored")
	}
	if _, err := s.Complete(SetupCompleteRequest{}, "", nil); err == nil {
		t.Fatal("database failure ignored")
	}
}
