package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestOriginEditorRemoteRoundTripAndConflict(t *testing.T) {
	var configuration map[string]any
	_ = json.Unmarshal([]byte(`{"originRequest":{"connectTimeout":17},"warp-routing":{"enabled":true},"ingress":[{"hostname":"vm.example.com","path":"/api/.*","service":"https://192.168.1.50:8443","originRequest":{"noTLSVerify":false,"originServerName":"app.internal","access":{"required":true}}},{"path":"/health","service":"http://server.local:8080"},{"service":"http_status:503"}]}`), &configuration)
	writes := 0
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if !strings.HasSuffix(r.URL.Path, "/configurations") {
			t.Fatal("unexpected API operation", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			writes++
			var payload struct {
				Config map[string]any `json:"config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			configuration = payload.Config
		}
		raw, _ := json.Marshal(configuration)
		return 200, `{"success":true,"result":{"config":` + string(raw) + `}}`
	})
	mgr := tunnelManager(t, api)
	identity, _ := mgr.TunnelIdentity()
	current, err := api.TunnelConfiguration(identity)
	if err != nil {
		t.Fatal(err)
	}
	revision := TunnelConfigurationRevision(current)
	current["ingress"].([]any)[0].(map[string]any)["service"] = "https://192.168.1.60:9443"
	if _, err := api.UpdateTunnelConfiguration(identity, current, revision); err != nil {
		t.Fatal(err)
	}
	if writes != 1 || !reflect.DeepEqual(configuration, current) {
		t.Fatal("configuration lost fields", configuration)
	}
	if _, err := api.UpdateTunnelConfiguration(identity, current, revision); !errors.Is(err, ErrTunnelConfigurationChanged) {
		t.Fatal("stale form accepted", err)
	}
	if writes != 1 {
		t.Fatal("stale form wrote routes")
	}
	if _, err := api.UpdateTunnelConfiguration(identity, map[string]any{}, TunnelConfigurationRevision(configuration)); err == nil {
		t.Fatal("empty config accepted")
	}
	// Failed Cloudflare fetch must never replace the known configuration.
	api.client.Transport = dnsTransport(func(r *http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if _, err := api.UpdateTunnelConfiguration(identity, current, TunnelConfigurationRevision(configuration)); err == nil {
		t.Fatal("offline write accepted")
	}
}

func TestOriginEditorLocalConflictAndBackup(t *testing.T) {
	api := dnsFixture(t, func(r *http.Request) (int, string) { t.Fatal("local save contacted Cloudflare"); return 500, "" })
	mgr := tunnelManager(t, api)
	before, _ := mgr.Read()
	next := "# exact external origin\ntunnel: local-id\ningress:\n  - hostname: vm.example.com\n    service: https://server.local:8443\n    originRequest:\n      noTLSVerify: false\n  - service: http_status:503\n"
	if err := mgr.WriteIfUnchanged(next, 0, &before); err != nil {
		t.Fatal(err)
	}
	actual, _ := mgr.Read()
	if actual != next {
		t.Fatal("rewrote operator config", actual)
	}
	backups, err := mgr.ListBackups(50, 0)
	if err != nil || len(backups) == 0 || backups[0].Content != before {
		t.Fatal("missing pre-edit backup", backups, err)
	}
	if err := mgr.WriteIfUnchanged(before, 0, &before); !errors.Is(err, ErrLocalConfigurationChanged) {
		t.Fatal("stale save accepted", err)
	}
	actual, _ = mgr.Read()
	if actual != next {
		t.Fatal("stale save erased current routes")
	}
	if err := mgr.Validate("ingress:\n  - path: /api\n    service: http://vm:80\n"); err == nil {
		t.Fatal("matched fallback accepted")
	}
	_ = os.Remove(mgr.cfg.CloudflaredConfig)
	if err := mgr.WriteIfUnchanged(next, 0, &next); !errors.Is(err, ErrLocalConfigurationChanged) {
		t.Fatal("deleted config was silently overwritten", err)
	}
}

func TestOriginProbeUsesExplicitTLSAndHostOptions(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "app.internal" {
			t.Errorf("Host override lost: %s", r.Host)
		}
		if r.TLS.ServerName != "app.internal" {
			t.Errorf("TLS server name lost: %s", r.TLS.ServerName)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	svc := &DockerContainers{}
	result, err := svc.ProbeTarget(server.URL)
	if err != nil || result["reachable"] != false {
		t.Fatal("HTTPS verification was bypassed by default", result, err)
	}
	result, err = svc.ProbeTargetWithOptions(server.URL, OriginProbeOptions{NoTLSVerify: true, HTTPHostHeader: "app.internal", OriginServerName: "app.internal"})
	if err != nil || result["reachable"] != true || result["status_code"] != 204 {
		t.Fatal(result, err)
	}
	result, _ = svc.ProbeTarget("file:///etc/passwd")
	if result["reachable"] != false {
		t.Fatal("non-HTTP probe accepted")
	}
}

func TestOriginEditorUsesCloudflareNormalizedRevision(t *testing.T) {
	configuration := map[string]any{"ingress": []any{map[string]any{"service": "http_status:404"}}}
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodPut {
			var input struct {
				Config map[string]any `json:"config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			configuration = input.Config
			configuration["originRequest"] = map[string]any{"connectTimeout": float64(30)}
		}
		raw, _ := json.Marshal(configuration)
		return 200, `{"success":true,"result":{"config":` + string(raw) + `}}`
	})
	identity, _ := tunnelManager(t, api).TunnelIdentity()
	current, _ := api.TunnelConfiguration(identity)
	accepted, err := api.UpdateTunnelConfiguration(identity, current, TunnelConfigurationRevision(current))
	if err != nil || accepted["originRequest"] == nil {
		t.Fatal("normalization omitted", accepted, err)
	}
	if _, err := api.UpdateTunnelConfiguration(identity, accepted, TunnelConfigurationRevision(accepted)); err != nil {
		t.Fatal("second save conflicts with our own first save", err)
	}
}
