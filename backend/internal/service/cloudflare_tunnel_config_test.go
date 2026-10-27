package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"docktun/internal/config"
)

func tunnelManager(t *testing.T, api *CloudflareAPI) *ConfigManager {
	t.Helper()
	directory := t.TempDir()
	mgr := NewConfigManager(&config.Config{CloudflaredConfig: filepath.Join(directory, "config.yml")}, api.db)
	if err := os.WriteFile(mgr.cfg.CloudflaredConfig, []byte("tunnel: stale-local-id\ningress:\n  - service: http_status:404\n"), 0600); err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"a":"account123","t":"real-token-tunnel","s":"private-secret-never-return"}`))
	if err := os.WriteFile(filepath.Join(directory, "tunnel.token"), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestTokenTunnelIdentityAndOverviewUseCloudflareRoutes(t *testing.T) {
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if !strings.Contains(r.URL.Path, "/accounts/account123/cfd_tunnel/real-token-tunnel") {
			t.Fatal("used stale local identity:", r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/configurations") {
			return 200, `{"success":true,"result":{"config":{"ingress":[{"hostname":"app.example.com","service":"http://app:80"},{"hostname":"other.example.com","service":"http://other:80"},{"service":"http_status:404"}]}}}`
		}
		return 200, `{"success":true,"result":{"id":"real-token-tunnel","status":"healthy"}}`
	})
	mgr := tunnelManager(t, api)
	identity, err := mgr.TunnelIdentity()
	if err != nil || !identity.Remote || identity.ID != "real-token-tunnel" || identity.AccountID != "account123" {
		t.Fatal(identity, err)
	}
	encoded, _ := json.Marshal(identity)
	if strings.Contains(string(encoded), "private-secret") {
		t.Fatal("leaked connector secret")
	}
	status, err := api.TunnelStatus(identity)
	if err != nil || status["status"] != "healthy" {
		t.Fatal(status, err)
	}
	svc := &RemoteAccessService{cfg: mgr.cfg, configMgr: mgr, cfAPISvc: api, runDocker: func(context.Context, ...string) ([]byte, error) { return nil, fmt.Errorf("No such object") }}
	overview, err := svc.GetOverview()
	if err != nil || overview.Cloudflare.TunnelID != identity.ID || !overview.Cloudflare.RoutesKnown || overview.Cloudflare.IngressCount != 2 || overview.Cloudflare.ConfigurationSource != "cloudflare" {
		t.Fatalf("wrong remote summary: %+v %v", overview, err)
	}
}

func TestRemoteExposurePreservesOtherRoutesAndSettings(t *testing.T) {
	var updated map[string]any
	writes := 0
	configuration := `{"originRequest":{"connectTimeout":17},"warp-routing":{"enabled":true},"ingress":[{"hostname":"other.example.com","service":"http://other:80","originRequest":{"httpHostHeader":"untouched"}},{"hostname":"app.example.com","path":"/special","service":"http://path-app:80"},{"hostname":"old.example.com","service":"http://app:80","originRequest":{"connectTimeout":5}},{"service":"http_status:503"}]}`
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/configurations") {
			if r.Method == "PUT" {
				writes++
				var payload struct {
					Config map[string]any `json:"config"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				updated = payload.Config
				raw, _ := json.Marshal(updated)
				return 200, `{"success":true,"result":{"config":` + string(raw) + `}}`
			}
			return 200, `{"success":true,"result":{"config":` + configuration + `}}`
		}
		if strings.HasSuffix(r.URL.Path, "/zones") {
			return 200, `{"success":true,"result":[{"id":"zone123","name":"example.com"}]}`
		}
		if r.Method == "GET" {
			return 200, `{"success":true,"result":[]}`
		}
		if strings.Contains(r.URL.Path, "/dns_records") {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "real-token-tunnel.cfargotunnel.com") {
				t.Fatal("DNS used stale tunnel:", string(body))
			}
			return 200, `{"success":true,"result":{"id":"new-record"}}`
		}
		t.Fatal(r.Method, r.URL)
		return 500, ""
	})
	mgr := tunnelManager(t, api)
	svc := &DockerContainers{db: api.db, configMgr: mgr, cfAPI: api}
	if err := svc.saveExposureToDB("app", ExposureSettings{Mode: "public", Hostname: "old.example.com"}); err != nil {
		t.Fatal(err)
	}
	before, _ := mgr.Read()
	if err := svc.syncCloudflareIngress("app", ExposureSettings{Mode: "public", Hostname: "app.example.com", Target: "http://app:8080"}); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatal("expected one Cloudflare configuration update")
	}
	after, _ := mgr.Read()
	if before != after {
		t.Fatal("remote operation rewrote local YAML")
	}
	var original map[string]any
	json.Unmarshal([]byte(configuration), &original)
	for _, key := range []string{"originRequest", "warp-routing"} {
		if !reflect.DeepEqual(original[key], updated[key]) {
			t.Fatal("lost global setting", key)
		}
	}
	rules := updated["ingress"].([]any)
	old := original["ingress"].([]any)
	if len(rules) != 4 || !reflect.DeepEqual(rules[0], old[0]) || !reflect.DeepEqual(rules[1], old[1]) || !reflect.DeepEqual(rules[3], old[3]) {
		t.Fatal("lost unrelated/path/catch-all rules", rules)
	}
	selected := rules[2].(map[string]any)
	if selected["hostname"] != "app.example.com" || selected["service"] != "http://app:8080" || selected["originRequest"] == nil {
		t.Fatal("selected app not updated correctly", selected)
	}
}

func TestRemoteConfigPermissionFailureDoesNotBecomeZeroOrWrite(t *testing.T) {
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if r.Method != "GET" {
			t.Fatal("write after permission failure")
		}
		return 403, `{"success":false,"errors":[{"message":"Insufficient permissions"}]}`
	})
	mgr := tunnelManager(t, api)
	svc := &DockerContainers{db: api.db, configMgr: mgr, cfAPI: api}
	before, _ := mgr.Read()
	if err := svc.syncCloudflareIngress("app", ExposureSettings{Mode: "public", Hostname: "app.example.com", Target: "http://app:80"}); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatal(err)
	}
	after, _ := mgr.Read()
	if before != after {
		t.Fatal("changed local config after failed remote request")
	}
	remote := &RemoteAccessService{cfg: mgr.cfg, configMgr: mgr, cfAPISvc: api, runDocker: func(context.Context, ...string) ([]byte, error) { return nil, fmt.Errorf("No such object") }}
	summary, err := remote.GetOverview()
	if err != nil || summary.Cloudflare.RoutesKnown || summary.Cloudflare.LastError == "" {
		t.Fatal("permission error reported as verified zero routes", summary, err)
	}
}

func TestInvalidConnectorNeverFallsBackToStaleLocalTunnel(t *testing.T) {
	api := dnsFixture(t, func(*http.Request) (int, string) { t.Fatal("unexpected API request"); return 500, "" })
	mgr := tunnelManager(t, api)
	if err := os.WriteFile(filepath.Join(filepath.Dir(mgr.cfg.CloudflaredConfig), "tunnel.token"), []byte("invalid-token"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, err := mgr.TunnelIdentity()
	if err == nil || !identity.Remote || identity.ID != "" {
		t.Fatal("invalid token fell back to stale local YAML", identity, err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(mgr.cfg.CloudflaredConfig), "tunnel.token")); err != nil {
		t.Fatal(err)
	}
	identity, err = mgr.TunnelIdentity()
	if err != nil || identity.Remote || identity.ID != "stale-local-id" {
		t.Fatal("local credentials mode was lost", identity, err)
	}
}

func TestRejectedRemoteWriteDoesNotAttemptDNS(t *testing.T) {
	writes := 0
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if !strings.HasSuffix(r.URL.Path, "/configurations") {
			t.Fatal("attempted DNS after failed tunnel write", r.URL)
		}
		if r.Method == "PUT" {
			writes++
			return 403, `{"success":false,"errors":[{"message":"Read-only token"}]}`
		}
		return 200, `{"success":true,"result":{"config":{"ingress":[{"service":"http_status:404"}]}}}`
	})
	mgr := tunnelManager(t, api)
	svc := &DockerContainers{db: api.db, configMgr: mgr, cfAPI: api}
	if err := svc.syncCloudflareIngress("app", ExposureSettings{Mode: "public", Hostname: "app.example.com", Target: "http://app:80"}); err == nil || !strings.Contains(err.Error(), "Read-only") {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatal("expected checked remote configuration write")
	}
}
