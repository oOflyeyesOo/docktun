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
				if args[0] == "inspect" {
					if installed {
						return []byte("existing"), nil
					}
					return nil, errors.New("not found")
				}
				return []byte("started"), nil
			}
			if err := s.TailscaleControl("start"); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(calls[len(calls)-1], " ")
			if installed {
				if got != "start docktun-tailscale" {
					t.Fatal(got)
				}
				return
			}
			for _, want := range []string{"type=volume,source=tester-tailscale-state,target=/var/lib/tailscale", "--network tester-network", "TS_USERSPACE=true", "TS_STATE_DIR=/var/lib/tailscale"} {
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
	s := &RemoteAccessService{runDocker: func(context.Context, ...string) ([]byte, error) { return nil, errors.New("not found") }}
	if err := s.TailscaleControl("start"); err == nil || !strings.Contains(err.Error(), "volume name") {
		t.Fatal(err)
	}
}

func TestSelectedProviderSurvivesRestart(t *testing.T) {
	s := setupFixture(t)
	s.cfg.DBPath = filepath.Join(s.dataDir, "app.db")
	s.cfg.CloudflaredConfig = filepath.Join(s.dataDir, "cloudflared.yml")
	if err := os.WriteFile(s.cfg.CloudflaredConfig, []byte("tunnel: leftover"), 0600); err != nil {
		t.Fatal(err)
	}
	s.meshSvc = NewMeshService(s.dataDir, nil, s.cfg)
	for _, provider := range []string{"tailscale", "wireguard", "none", "cloudflare"} {
		if _, err := s.applyConfiguration(SetupCompleteRequest{Tunnel: SetupTunnelRequest{Type: provider}}, 1, ""); err != nil {
			t.Fatal(err)
		}
		s.meshSvc = NewMeshService(s.dataDir, nil, s.cfg)
		status, err := s.GetStatus(false)
		if err != nil || status.TunnelType != provider {
			t.Fatalf("%s: %+v %v", provider, status, err)
		}
		if provider != "cloudflare" {
			state := NewCloudflared(s.cfg, nil).Status()
			if state.Status != "disabled" || state.LastError != "" {
				t.Fatal(state)
			}
		}
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
