package service

import (
	"strings"
	"testing"
)

func TestRenderCaddyfile(t *testing.T) {
	cfg := LocalProxyConfig{Routes: []LocalProxyRoute{
		{Host: "app.home", Target: "my-app", Port: 8080, Enabled: true},
		{Host: "disabled.home", Target: "other", Port: 80, Enabled: false},
	}}
	got := renderCaddyfile(cfg)
	if !strings.Contains(got, "app.home") || !strings.Contains(got, "reverse_proxy my-app:8080") || strings.Contains(got, "disabled.home") {
		t.Fatalf("unexpected Caddyfile: %q", got)
	}
}

func TestRenderProxyComposeUsesExternalNetwork(t *testing.T) {
	got := renderProxyCompose(LocalProxyConfig{HTTPPort: 8080, HTTPSPort: 8443, Network: "docktun"})
	if !strings.Contains(got, "container_name: docktun-local-proxy") || !strings.Contains(got, "external: true") || !strings.Contains(got, "name: docktun") {
		t.Fatalf("unexpected proxy Compose: %q", got)
	}
}

func TestRenderProxyEngines(t *testing.T) {
	for _, engine := range []string{"caddy", "traefik", "nginx"} {
		cfg := LocalProxyConfig{Engine: engine, HTTPPort: 8080, HTTPSPort: 8443, Network: "docktun-proxy"}
		got := renderProxyCompose(cfg)
		if !strings.Contains(got, "name: docktun-proxy") || !strings.Contains(got, "docktun-local-proxy") {
			t.Fatalf("%s compose is missing the dedicated proxy network or container: %q", engine, got)
		}
		if !strings.Contains(renderProxyConfig(cfg), "http") && engine != "caddy" {
			t.Fatalf("%s configuration was not rendered", engine)
		}
	}
}
