package service

import (
	"context"
	"docktun/internal/config"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderMountUsesActualHostSource(t *testing.T) {
	var own updateContainer
	json.Unmarshal([]byte(`{"Mounts":[{"Type":"bind","Source":"/volume1/docker/docktun","Destination":"/app/data"},{"Type":"volume","Source":"/var/lib/docker/volumes/custom_cf/_data","Name":"custom_cf","Destination":"/etc/cloudflared"}]}`), &own)
	for _, tc := range []struct{ dir, target, want string }{
		{"/etc/cloudflared", "/etc/cloudflared", "type=volume,source=custom_cf,target=/etc/cloudflared,readonly"},
		{"/app/data/wireguard", "/config", "type=bind,source=/volume1/docker/docktun/wireguard,target=/config,readonly"},
	} {
		got, err := providerMount(own, tc.dir, tc.target, true)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.dir, got, err)
		}
	}
	if _, err := providerMount(own, "/missing", "/config", false); err == nil {
		t.Fatal("invented a host path")
	}
	if _, err := providerMount(own, "/app/database", "/config", false); err == nil {
		t.Fatal("accepted sibling prefix")
	}
}

func TestProvidersStageStoppedAndOperateIndependently(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DBPath: filepath.Join(dir, "app.db"), CloudflaredConfig: filepath.Join(dir, "config.yml")}
	cfd := NewCloudflared(cfg, nil)
	if err := cfd.SaveConnectorToken("private-test-connector-token"); err != nil {
		t.Fatal(err)
	}
	p := cfd.providers
	hostname, _ := os.Hostname()
	own := map[string]any{"Config": map[string]any{"Hostname": hostname}, "State": map[string]bool{"Running": true}, "Mounts": []map[string]string{{"Type": "bind", "Source": "/volume1/docktun", "Destination": dir}}}
	containers := map[string]map[string]any{}
	var calls []string
	p.run = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "inspect":
			if args[1] == hostname {
				return json.Marshal([]any{own})
			}
			if c, ok := containers[args[1]]; ok {
				return json.Marshal([]any{c})
			}
			return nil, fmt.Errorf("No such container")
		case "create":
			labels := map[string]any{}
			for i := range args {
				if args[i] == "--label" {
					k, v, _ := strings.Cut(args[i+1], "=")
					labels[k] = v
				}
			}
			containers[args[2]] = map[string]any{"Config": map[string]any{"Labels": labels}, "State": map[string]bool{"Running": false}}
		case "start", "restart", "stop":
			containers[args[1]]["State"] = map[string]bool{"Running": args[0] != "stop"}
		}
		return []byte("ok"), nil
	}
	for _, provider := range []string{"cloudflare", "tailscale", "wireguard"} {
		if err := p.Prepare(provider); err != nil {
			t.Fatal(err)
		}
	}
	if len(containers) != 3 {
		t.Fatal(containers)
	}
	for name, c := range containers {
		if c["State"].(map[string]bool)["Running"] {
			t.Fatal("started without consent", name)
		}
	}
	staged := strings.Join(calls, "\n")
	if strings.Contains(staged, "private-test-connector-token") || strings.Contains(staged, "run -d") {
		t.Fatal("leaked token or started during preparation")
	}
	for _, provider := range []string{"tailscale", "cloudflare", "wireguard"} {
		if err := p.Control(provider, "start"); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Control("cloudflare", "stop"); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"tailscale", "wireguard"} {
		if !containers[p.name(provider)]["State"].(map[string]bool)["Running"] {
			t.Fatal("stopped another provider", provider)
		}
	}
	delete(containers, p.name("cloudflare"))
	if err := p.Control("cloudflare", "restart"); err != nil {
		t.Fatal(err)
	}
	if !containers[p.name("cloudflare")]["State"].(map[string]bool)["Running"] {
		t.Fatal("restart did not create missing connector")
	}
}

func TestEditingExposureDoesNotEnableStoppedCloudflare(t *testing.T) {
	setup := setupFixture(t)
	if _, err := setup.Complete(SetupCompleteRequest{SetupToken: setup.cfg.SetupToken, AdminPassword: "strong-password"}, "", nil); err != nil {
		t.Fatal(err)
	}
	setup.cfg.CloudflaredConfig = filepath.Join(setup.dataDir, "config.yml")
	if err := os.WriteFile(setup.cfg.CloudflaredConfig, []byte("tunnel: test\ningress:\n  - hostname: app.example.test\n    service: http://app:80\n  - service: http_status:404\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfd := NewCloudflared(setup.cfg, nil)
	cfd.providers.run = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] != "inspect" {
			t.Fatalf("editing exposure started a stopped provider: %v", args)
		}
		return []byte("created 0001-01-01T00:00:00Z 0"), nil
	}
	svc := &DockerContainers{cfg: setup.cfg, configMgr: NewConfigManager(setup.cfg, setup.db), cloudflared: cfd}
	if err := svc.syncCloudflareIngress("app", ExposureSettings{Mode: "internal"}); err != nil {
		t.Fatal(err)
	}
}

func TestStoppedCloudflareIsNotReportedAsFailure(t *testing.T) {
	for _, exit := range []string{"created 0001-01-01T00:00:00Z 0", "exited 2026-10-26T00:00:00Z 137"} {
		cfd := NewCloudflared(&config.Config{}, nil)
		cfd.providers.run = func(_ context.Context, args ...string) ([]byte, error) { return []byte(exit), nil }
		got := cfd.Status()
		if got.Running || got.Status != "stopped" || got.LastError != "" {
			t.Fatalf("stopped connector shown as failed: %+v", got)
		}
	}
}
