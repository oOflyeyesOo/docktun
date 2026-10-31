package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type LocalProxyRoute struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	Target  string `json:"target"`
	Port    int    `json:"port"`
	Enabled bool   `json:"enabled"`
}

type LocalProxyConfig struct {
	Engine      string            `json:"engine"`
	Enabled     bool              `json:"enabled"`
	HTTPPort    int               `json:"http_port"`
	HTTPSPort   int               `json:"https_port"`
	Network     string            `json:"network"`
	Routes      []LocalProxyRoute `json:"routes"`
	Running     bool              `json:"running"`
	ComposeFile string            `json:"-"`
	ConfigFile  string            `json:"-"`
}

type LocalProxy struct {
	dir string
}

func NewLocalProxy(dataDir string) *LocalProxy {
	return &LocalProxy{dir: filepath.Join(dataDir, "local-proxy")}
}

func (p *LocalProxy) config() LocalProxyConfig {
	return LocalProxyConfig{Engine: "caddy", HTTPPort: 80, HTTPSPort: 443, Network: "docktun-proxy", Routes: []LocalProxyRoute{}}
}

func (p *LocalProxy) read() (LocalProxyConfig, error) {
	cfg := p.config()
	raw, err := os.ReadFile(filepath.Join(p.dir, "config.json"))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid local proxy configuration: %w", err)
	}
	if cfg.HTTPPort == 0 {
		cfg.HTTPPort = 80
	}
	if cfg.Engine == "" {
		cfg.Engine = "caddy"
	}
	if !supportedProxyEngine(cfg.Engine) {
		return cfg, fmt.Errorf("unsupported local proxy engine %q", cfg.Engine)
	}
	if cfg.HTTPSPort == 0 {
		cfg.HTTPSPort = 443
	}
	if cfg.Network == "" {
		cfg.Network = "docktun-proxy"
	} else if cfg.Network == "docktun" {
		// Migrate the original default to the dedicated proxy network.
		cfg.Network = "docktun-proxy"
	}
	if cfg.Routes == nil {
		cfg.Routes = []LocalProxyRoute{}
	}
	return cfg, nil
}

func (p *LocalProxy) Status(ctx context.Context) (LocalProxyConfig, error) {
	cfg, err := p.read()
	if err != nil {
		return cfg, err
	}
	cfg.ComposeFile = filepath.Join(p.dir, "docker-compose.yml")
	cfg.ConfigFile = filepath.Join(p.dir, proxyConfigFilename(cfg.Engine))
	if !cfg.Enabled {
		return cfg, nil
	}
	out, err := exec.CommandContext(ctx, "docker", "ps", "--filter", "name=docktun-local-proxy", "--format", "{{.Names}}").Output()
	cfg.Running = err == nil && strings.Contains(string(out), "docktun-local-proxy")
	return cfg, nil
}

func (p *LocalProxy) Apply(ctx context.Context, input LocalProxyConfig) (LocalProxyConfig, error) {
	if input.Engine == "" {
		input.Engine = "caddy"
	}
	if !supportedProxyEngine(input.Engine) {
		return input, fmt.Errorf("unsupported local proxy engine %q", input.Engine)
	}
	if input.HTTPPort < 1 || input.HTTPPort > 65535 || input.HTTPSPort < 1 || input.HTTPSPort > 65535 {
		return input, fmt.Errorf("proxy ports must be between 1 and 65535")
	}
	if input.Network == "" {
		input.Network = "docktun"
	}
	if !validProxyToken(input.Network, false) {
		return input, fmt.Errorf("proxy network contains unsupported characters")
	}
	if input.Routes == nil {
		input.Routes = []LocalProxyRoute{}
	}
	for i := range input.Routes {
		r := &input.Routes[i]
		if r.ID == "" {
			r.ID = fmt.Sprintf("route-%d", i+1)
		}
		if r.Host == "" || r.Target == "" || r.Port < 1 || r.Port > 65535 {
			return input, fmt.Errorf("each enabled route needs a host, target, and valid port")
		}
		if r.Enabled && (!validProxyToken(r.Host, true) || !validProxyToken(r.Target, false)) {
			return input, fmt.Errorf("proxy route host and target contain unsupported characters")
		}
	}
	if err := os.MkdirAll(p.dir, 0755); err != nil {
		return input, err
	}
	if err := os.WriteFile(filepath.Join(p.dir, "config.json"), mustJSON(input), 0600); err != nil {
		return input, err
	}
	if err := os.WriteFile(filepath.Join(p.dir, proxyConfigFilename(input.Engine)), []byte(renderProxyConfig(input)), 0600); err != nil {
		return input, err
	}
	compose := renderProxyCompose(input)
	if err := os.WriteFile(filepath.Join(p.dir, "docker-compose.yml"), []byte(compose), 0600); err != nil {
		return input, err
	}
	if input.Enabled {
		if err := ensureProxyNetwork(ctx, input.Network); err != nil {
			return input, err
		}
		for _, route := range input.Routes {
			if route.Enabled {
				if err := connectProxyTarget(ctx, input.Network, route.Target); err != nil {
					return input, err
				}
			}
		}
		if err := runProxyCompose(ctx, p.dir, "up", "-d"); err != nil {
			return input, err
		}

	} else {
		if err := runProxyCompose(ctx, p.dir, "down"); err != nil {
			return input, err
		}
	}
	return p.Status(ctx)
}

func validProxyToken(value string, hostname bool) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || (hostname && char == '.') {
			continue
		}
		return false
	}
	return true
}

func supportedProxyEngine(engine string) bool {
	return engine == "caddy" || engine == "traefik" || engine == "nginx"
}

func proxyConfigFilename(engine string) string {
	switch engine {
	case "traefik":
		return "dynamic.yml"
	case "nginx":
		return "nginx.conf"
	default:
		return "Caddyfile"
	}
}

func ensureProxyNetwork(ctx context.Context, network string) error {
	if err := exec.CommandContext(ctx, "docker", "network", "inspect", network).Run(); err == nil {
		return nil
	}
	if output, err := exec.CommandContext(ctx, "docker", "network", "create", network).CombinedOutput(); err != nil {
		return fmt.Errorf("cannot create local proxy network: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func connectProxyTarget(ctx context.Context, network, target string) error {
	if err := exec.CommandContext(ctx, "docker", "inspect", target).Run(); err != nil {
		return fmt.Errorf("proxy target container %q was not found", target)
	}
	if err := exec.CommandContext(ctx, "docker", "network", "connect", network, target).Run(); err != nil {
		membership, inspectErr := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .NetworkSettings.Networks}}", target).Output()
		if inspectErr != nil || !strings.Contains(string(membership), `"`+network+`"`) {
			return fmt.Errorf("could not connect proxy target %q to network %q", target, network)
		}
	}
	return nil
}

func mustJSON(value any) []byte {
	raw, _ := json.MarshalIndent(value, "", "  ")
	return append(raw, '\n')
}

func renderProxyConfig(cfg LocalProxyConfig) string {
	switch cfg.Engine {
	case "traefik":
		return renderTraefikConfig(cfg)
	case "nginx":
		return renderNginxConfig(cfg)
	default:
		return renderCaddyfile(cfg)
	}
}

func renderCaddyfile(cfg LocalProxyConfig) string {
	var out strings.Builder
	for _, route := range cfg.Routes {
		if !route.Enabled {
			continue
		}
		fmt.Fprintf(&out, "%s {\n\treverse_proxy %s:%d\n}\n\n", route.Host, route.Target, route.Port)
	}
	return out.String()
}

func renderProxyCompose(cfg LocalProxyConfig) string {
	image, service, configMount := "caddy:2-alpine", "caddy", "./Caddyfile:/etc/caddy/Caddyfile:ro"
	switch cfg.Engine {
	case "traefik":
		image, service, configMount = "traefik:v3.0", "traefik", "./dynamic.yml:/etc/traefik/dynamic.yml:ro"
	case "nginx":
		image, service, configMount = "nginx:1.27-alpine", "nginx", "./nginx.conf:/etc/nginx/nginx.conf:ro"
	}
	var out strings.Builder
	fmt.Fprintf(&out, `services:
  %s:
    image: %s
    container_name: docktun-local-proxy
    restart: unless-stopped
    ports:
      - "%d:80"
      - "%d:443"
    volumes:
      - %s
`, service, image, cfg.HTTPPort, cfg.HTTPSPort, configMount)
	if cfg.Engine == "traefik" {
		out.WriteString("    command:\n      - --providers.file.filename=/etc/traefik/dynamic.yml\n      - --providers.file.watch=true\n")
	}
	out.WriteString(`    networks:
      - proxy
networks:
  proxy:
    external: true
    name: `)
	out.WriteString(cfg.Network)
	out.WriteString("\n")
	if cfg.Engine == "caddy" {
		out.WriteString(`volumes:
  caddy_data:
  caddy_config:
`)
	}
	return out.String()
}

func renderTraefikConfig(cfg LocalProxyConfig) string {
	var out strings.Builder
	out.WriteString("http:\n  routers:\n")
	for i, route := range cfg.Routes {
		if route.Enabled {
			fmt.Fprintf(&out, "    route-%d:\n      rule: Host(`%s`)\n      service: service-%d\n", i, route.Host, i)
		}
	}
	out.WriteString("  services:\n")
	for i, route := range cfg.Routes {
		if route.Enabled {
			fmt.Fprintf(&out, "    service-%d:\n      loadBalancer:\n        servers:\n          - url: http://%s:%d\n", i, route.Target, route.Port)
		}
	}
	return out.String()
}

func renderNginxConfig(cfg LocalProxyConfig) string {
	var out strings.Builder
	out.WriteString("events {}\nhttp {\n")
	for _, route := range cfg.Routes {
		if route.Enabled {
			fmt.Fprintf(&out, "  server { listen 80; server_name %s; location / { proxy_pass http://%s:%d; } }\n", route.Host, route.Target, route.Port)
		}
	}
	out.WriteString("}\n")
	return out.String()
}

func runProxyCompose(ctx context.Context, dir string, args ...string) error {
	command := append([]string{"compose", "-p", "docktun-local-proxy", "-f", "docker-compose.yml"}, args...)
	cmd := exec.CommandContext(ctx, "docker", command...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("local proxy Compose failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
