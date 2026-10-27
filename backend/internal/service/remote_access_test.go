package service

import (
	"context"
	"docktun/internal/config"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailscaleControlMountAndReuse(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{true: "compose container", false: "new container"}[installed], func(t *testing.T) {
			t.Setenv("TAILSCALE_STATE_VOLUME", "tester-tailscale-state")
			t.Setenv("TAILSCALE_NETWORK", "tester-network")
			var calls [][]string
			s := &RemoteAccessService{dataDir: "/app/data"}
			s.runDocker = func(_ context.Context, args ...string) ([]byte, error) {
				calls = append(calls, args)
				if args[0] == "network" && args[1] == "ls" {
					if strings.Contains(strings.Join(args, " "), "tester-network") {
						return []byte("custom-network-id\ttester-network"), nil
					}
					return []byte("default-network-id\tdocktun"), nil
				}
				if args[0] == "inspect" {
					if installed {
						return []byte(`[{"State":{"Running":false}}]`), nil
					}
					return nil, errors.New("not found")
				}
				return []byte("started"), nil
			}
			if err := s.TailscaleControl("start"); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(calls[len(calls)-1], " ")
			if !installed {
				for _, call := range calls {
					if call[0] == "create" {
						got = strings.Join(call, " ")
					}
				}
			}
			if installed {
				if got != "start docktun-tailscale" {
					t.Fatal(got)
				}
				return
			}
			for _, want := range []string{"type=volume,source=tester-tailscale-state,target=/var/lib/tailscale", "--network custom-network-id", "TS_USERSPACE=true", "TS_STATE_DIR=/var/lib/tailscale"} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %s: %s", want, got)
				}
			}
			if strings.Contains(got, "/app/data") || strings.Contains(got, "NET_ADMIN") {
				t.Fatal(got)
			}
		})
	}
}

func TestTailscaleRejectsContainerPath(t *testing.T) {
	t.Setenv("TAILSCALE_STATE_VOLUME", "/app/data/tailscale")
	s := &RemoteAccessService{runDocker: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "inspect" {
			return nil, errors.New("not found")
		}
		return []byte("ok"), nil
	}}
	if err := s.TailscaleControl("start"); err == nil || !strings.Contains(err.Error(), "volume name") {
		t.Fatal(err)
	}
}

func TestAccountCreationDoesNotSelectProvider(t *testing.T) {
	s := setupFixture(t)
	s.cfg.DBPath = filepath.Join(s.dataDir, "app.db")
	s.meshSvc = NewMeshService(s.dataDir, nil, s.cfg)
	original := &MeshConfig{Mode: "tailscale", Enabled: true}
	if err := s.meshSvc.Save(original); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyConfiguration(SetupCompleteRequest{Tunnel: SetupTunnelRequest{Type: "cloudflare"}}, 1, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.meshSvc.Load()
	if err != nil || got.Mode != "tailscale" {
		t.Fatalf("account creation changed provider: %+v %v", got, err)
	}
}

func TestMeshDefaultAndCorruption(t *testing.T) {
	dir := t.TempDir()
	s := NewMeshService(dir, nil, &config.Config{})
	cfg, err := s.Load()
	if err != nil || cfg.Mode != "none" {
		t.Fatalf("%+v %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mesh.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("corrupt selection silently accepted")
	}
}
