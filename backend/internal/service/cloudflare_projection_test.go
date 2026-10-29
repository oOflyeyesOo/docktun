package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestColdCacheDisableDiscoversAndRemovesRemoteOnlyRoute(t *testing.T) {
	var written map[string]any
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodPut {
			var payload struct { Config map[string]any `json:"config"` }
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { t.Fatal(err) }
			written = payload.Config
			return 200, `{"success":true,"result":{}}`
		}
		return 200, `{"success":true,"result":{"config":{"ingress":[{"hostname":"discovered.example.com","service":"http://app:80"},{"hostname":"discovered.example.com","path":"/special","service":"http://app:90"},{"hostname":"other.example.com","service":"http://other:80"},{"service":"http_status:404"}]}}}`
	})
	mgr := tunnelManager(t, api)
	svc := &DockerContainers{db:api.db,configMgr:mgr,cfAPI:api,exposureDocker:func(_ context.Context,args ...string)([]byte,error){
		if strings.Join(args," ")!="inspect --format {{.Name}} app" { t.Fatalf("changed unrelated Docker resources: %v", args) }
		return []byte("/app\n"),nil
	}}
	if len(api.tunnelCache)!=0 { t.Fatal("test did not start with cold inventory") }
	if err := svc.UpdateExposure("app",ExposureSettings{Mode:"internal"}); err != nil {t.Fatal(err)}
	if written==nil {t.Fatal("reported internal access without removing existing remote route")}
	rules:=written["ingress"].([]any)
	if len(rules)!=3 || rules[0].(map[string]any)["path"]!="/special" || rules[1].(map[string]any)["hostname"]!="other.example.com" {t.Fatalf("lost other/path routes: %#v",rules)}
	if got:=svc.getExposureFromDB("app");got.Mode!="internal" {t.Fatalf("disabled mode not saved: %#v",got)}
}

func TestUnusedCloudflareDoesNotBlockInternalAccess(t *testing.T) {
	setup:=setupFixture(t)
	svc:=&DockerContainers{db:setup.db,exposureDocker:func(_ context.Context,args ...string)([]byte,error){
		if strings.Join(args," ")!="inspect --format {{.Name}} app" {t.Fatalf("unused provider was touched: %v",args)}
		return []byte("/app"),nil
	}}
	if err:=svc.UpdateExposure("app",ExposureSettings{Mode:"internal"});err!=nil {t.Fatal(err)}
}

func TestConfiguredCloudflareLookupFailureDoesNotReportDisabled(t *testing.T) {
	api:=dnsFixture(t,func(*http.Request)(int,string){return 403,`{"success":false,"errors":[{"message":"Insufficient permissions"}]}`})
	mgr:=tunnelManager(t,api)
	svc:=&DockerContainers{db:api.db,configMgr:mgr,cfAPI:api,exposureDocker:func(_ context.Context,args ...string)([]byte,error){return []byte("/app"),nil}}
	previous:=ExposureSettings{Mode:"private",PrivateType:"wireguard",PrivateHost:"10.1.1.2",PrivatePort:80}
	if err:=svc.saveExposureToDB("app",previous);err!=nil {t.Fatal(err)}
	err:=svc.UpdateExposure("app",ExposureSettings{Mode:"internal"})
	if err==nil || !strings.Contains(err.Error(),"verify existing Cloudflare routes") || !strings.Contains(err.Error(),"permissions") {t.Fatalf("lookup failure was hidden: %v",err)}
	if svc.getExposureFromDB("app").Mode!="private" {t.Fatal("failed verification changed selected provider")}
}

func TestRemoteProjectionIgnoresInactiveYAMLAndKeepsMetadata(t *testing.T) {
	api := dnsFixture(t, func(*http.Request) (int, string) {
		return 200, `{"success":true,"result":{"config":{"ingress":[{"hostname":"real.example.com","service":"http://app:8080"},{"hostname":"real.example.com","path":"/special","service":"http://app:9000"},{"hostname":"discovered.example.com","service":"http://remote-only:80"},{"service":"http_status:404"}]}}}`
	})
	mgr := tunnelManager(t, api)
	if err := os.WriteFile(mgr.cfg.CloudflaredConfig, []byte("tunnel: stale\ningress:\n  - hostname: stale.example.com\n    service: http://app:80\n  - hostname: inactive.example.com\n    service: http://inactive:80\n  - service: http_status:404\n"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, _ := mgr.TunnelIdentity()
	if _, err := api.TunnelConfiguration(identity); err != nil {
		t.Fatal(err)
	}
	svc := &DockerContainers{db: api.db, configMgr: mgr, cfAPI: api}
	saved := ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivateHost: "app.tailnet", PrivatePort: 80, TailscaleNode: tailscaleNodeName("app"), ZeroTrustEnabled: true, ZeroTrustEmails: "owner@example.com", ZeroTrustAuthType: "email", ZeroTrustSession: "24h", ZeroTrustAppID: "access-app", Notes: "keep me"}
	if err := svc.saveExposureToDB("app", saved); err != nil {
		t.Fatal(err)
	}
	routes := svc.loadCloudflareIngressMap()
	if _, found := routes["inactive"]; found {
		t.Fatal("inactive YAML leaked into token-managed inventory")
	}
	got := projectExposure(svc.getExposureFromDB("app"), routes["app"], true)
	if got.Mode != "public" || got.Hostname != "real.example.com" || got.Target != "http://app:8080" || got.PrivateType != saved.PrivateType || got.TailscaleNode != saved.TailscaleNode || got.ZeroTrustAppID != saved.ZeroTrustAppID || got.Notes != saved.Notes {
		t.Fatalf("incorrect projection: %#v", got)
	}
	var container Container
	setContainerExposure(&container, got)
	if !container.ZeroTrustEnabled || container.ZeroTrustEmails != saved.ZeroTrustEmails || container.PrivatePort != 80 || container.Hostname != got.Hostname {
		t.Fatalf("inventory lost metadata: %#v", container)
	}
	remote, found := routes["remote-only"]
	if !found || projectExposure(ExposureSettings{}, remote, found).Mode != "public" {
		t.Fatal("remote-only app was not registered")
	}
	if svc.getExposureFromDB("app").Mode != "private" {
		t.Fatal("read-only projection modified selected provider")
	}
}

func TestRemoteDNSFailureStillProjectsAcceptedRoute(t *testing.T) {
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/configurations") {
			if r.Method == http.MethodPut {
				return 200, `{"success":true,"result":{}}`
			}
			return 200, `{"success":true,"result":{"config":{"ingress":[{"service":"http_status:404"}]}}}`
		}
		return 403, `{"success":false,"errors":[{"message":"Insufficient zone permissions"}]}`
	})
	mgr := tunnelManager(t, api)
	svc := &DockerContainers{db: api.db, configMgr: mgr, cfAPI: api}
	if err := svc.saveExposureToDB("app", ExposureSettings{Mode: "private", PrivateType: "tailscale", PrivatePort: 80}); err != nil {
		t.Fatal(err)
	}
	err := svc.syncCloudflareIngress("app", ExposureSettings{Mode: "public", Hostname: "partial.example.com", Target: "http://app:8080"})
	if err == nil || !strings.Contains(err.Error(), "hostname route saved") || !strings.Contains(err.Error(), "Zone DNS") {
		t.Fatalf("partial DNS error was hidden: %v", err)
	}
	var partial *cloudflareDNSPartialError
	if !errors.As(err, &partial) || !strings.Contains(errors.Unwrap(partial).Error(), "HTTP 403") {
		t.Fatalf("accepted-route DNS failure lost its typed cause: %v", err)
	}
	route, found := svc.loadCloudflareIngressMap()["app"]
	if !found || route.Hostname != "partial.example.com" {
		t.Fatalf("accepted remote route was hidden: %#v", route)
	}
	if svc.getExposureFromDB("app").Mode != "private" {
		t.Fatal("partial failure overwrote selected provider")
	}
}

func TestDisablingDiscoveredRemoteRoutePreservesOtherHostsAndPaths(t *testing.T) {
	var written map[string]any
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodPut {
			var payload struct {
				Config map[string]any `json:"config"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			written = payload.Config
			return 200, `{"success":true,"result":{}}`
		}
		return 200, `{"success":true,"result":{"config":{"ingress":[{"hostname":"selected.example.com","service":"http://app:80"},{"hostname":"other.example.com","service":"http://app:80"},{"hostname":"selected.example.com","path":"/special","service":"http://app:90"},{"service":"http_status:503"}]}}}`
	})
	mgr := tunnelManager(t, api)
	svc := &DockerContainers{db: api.db, configMgr: mgr, cfAPI: api}
	if err := svc.syncCloudflareIngress("app", ExposureSettings{Mode: "internal", previousCloudflareHost: "selected.example.com", previousCloudflareTarget: "http://app:80"}); err != nil {
		t.Fatal(err)
	}
	rules := written["ingress"].([]any)
	if len(rules) != 3 || rules[0].(map[string]any)["hostname"] != "other.example.com" || rules[1].(map[string]any)["path"] != "/special" || rules[2].(map[string]any)["service"] != "http_status:503" {
		t.Fatalf("removed unrelated routes: %#v", rules)
	}
}

func TestTunnelSnapshotEarlierReadCannotReplaceAcceptedWrite(t *testing.T) {
	api := &CloudflareAPI{}
	id := TunnelIdentity{ID: "tunnel", AccountID: "account", Remote: true}
	oldVersion := api.tunnelMutation(id)
	config := func(host string) map[string]any {
		return map[string]any{"ingress": []any{map[string]any{"hostname": host, "service": "http://app:80"}}}
	}
	api.rememberTunnelIngress(id, config("new.example.com"), nil)
	api.rememberTunnelIngress(id, config("old.example.com"), &oldVersion)
	if got := api.cachedTunnelIngress(id)["app"].Hostname; got != "new.example.com" {
		t.Fatal("old read replaced successful write:", got)
	}
}

func TestDisablingStoredHostDoesNotRemoveRepointedRemoteRoute(t *testing.T) {
	for _, previousTarget := range []string{"", "http://app:80"} {
		t.Run(previousTarget, func(t *testing.T) {
			var written map[string]any
			api := dnsFixture(t, func(r *http.Request) (int, string) {
				if r.Method == http.MethodPut {
					var payload struct {
						Config map[string]any `json:"config"`
					}
					json.NewDecoder(r.Body).Decode(&payload)
					written = payload.Config
					return 200, `{"success":true,"result":{}}`
				}
				return 200, `{"success":true,"result":{"config":{"ingress":[{"hostname":"old.example.com","service":"http://other-app:80"},{"service":"http_status:404"}]}}}`
			})
			mgr := tunnelManager(t, api)
			svc := &DockerContainers{db: api.db, configMgr: mgr, cfAPI: api}
			if err := svc.saveExposureToDB("app", ExposureSettings{Mode: "public", Hostname: "old.example.com", Target: previousTarget}); err != nil {
				t.Fatal(err)
			}
			if err := svc.syncCloudflareIngress("app", ExposureSettings{Mode: "internal"}); err != nil {
				t.Fatal(err)
			}
			rules := written["ingress"].([]any)
			if len(rules) != 2 || rules[0].(map[string]any)["service"] != "http://other-app:80" {
				t.Fatalf("removed externally repointed route: %#v", rules)
			}
		})
	}
}

func TestColdRemoteInventoryRefreshIsNonblockingAndShared(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	api := dnsFixture(t, func(*http.Request) (int, string) { t.Fatal("unexpected transport"); return 500, "" })
	api.client = &http.Client{Transport: dnsTransport(func(*http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"result":{"config":{"ingress":[{"hostname":"app.example.com","service":"http://app:80"}]}}}`))}, nil
	})}
	id := TunnelIdentity{ID: "tunnel", AccountID: "account123", Remote: true}
	start := time.Now()
	for i := 0; i < 30; i++ {
		api.cachedTunnelIngress(id)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("inventory waited for remote API")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	if requests.Load() != 1 {
		t.Fatal("duplicate background refreshes:", requests.Load())
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if api.cachedTunnelIngress(id)["app"].Hostname == "app.example.com" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background refresh did not register remote app")
}
