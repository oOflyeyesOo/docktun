package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"docktun/internal/config"
	"gopkg.in/yaml.v3"
)

// Docker owns each provider's running/stopped state independently. Legacy mesh
// selection is never used to gate another provider or start it implicitly.
type ProviderContainers struct {
	cfg *config.Config
	run func(context.Context, ...string) ([]byte, error)
}

var providerLocks [3]sync.Mutex

func providerLock(provider string) *sync.Mutex {
	if provider == "tailscale" {
		return &providerLocks[1]
	}
	if provider == "wireguard" {
		return &providerLocks[2]
	}
	return &providerLocks[0]
}

func NewProviderContainers(cfg *config.Config) *ProviderContainers {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &ProviderContainers{cfg: cfg, run: providerDocker}
}

func providerDocker(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("Docker %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (p *ProviderContainers) name(provider string) string {
	if provider == "cloudflare" && p.cfg.CloudflaredContainer != "" {
		return p.cfg.CloudflaredContainer
	}
	if provider == "cloudflare" {
		return "docktun-cloudflared"
	}
	return "docktun-" + provider
}

func (p *ProviderContainers) inspect(ctx context.Context, name string) (updateContainer, error) {
	out, err := p.run(ctx, "inspect", name)
	var items []updateContainer
	if err != nil {
		return updateContainer{}, err
	}
	if json.Unmarshal(out, &items) != nil || len(items) != 1 {
		return updateContainer{}, fmt.Errorf("invalid Docker container inspection")
	}
	return items[0], nil
}

func (p *ProviderContainers) self(ctx context.Context) (updateContainer, error) {
	hostname, _ := os.Hostname()
	ident := os.Getenv("DOCKTUN_CONTAINER")
	if ident == "" {
		ident = hostname
	}
	own, err := p.inspect(ctx, ident)
	if err != nil {
		return own, fmt.Errorf("cannot inspect DockTun's mounts; mount the Docker socket and set DOCKTUN_CONTAINER for a custom hostname: %w", err)
	}
	if own.Config["Hostname"] != hostname || !own.State.Running {
		return own, fmt.Errorf("DOCKTUN_CONTAINER must identify this running DockTun instance")
	}
	return own, nil
}

// Resolve actual daemon-host mount sources, including Synology bind paths and
// Compose-scoped named volumes. Never pass a container-only path to the daemon.
func providerMount(own updateContainer, directory, target string, readOnly bool) (string, error) {
	directory = path.Clean(directory)
	best := -1
	for i, m := range own.Mounts {
		destination := path.Clean(m.Destination)
		if directory == destination || strings.HasPrefix(directory, destination+"/") {
			if best < 0 || len(destination) > len(own.Mounts[best].Destination) {
				best = i
			}
		}
	}
	if best < 0 {
		return "", fmt.Errorf("mount persistent storage at %s in DockTun before enabling this provider", directory)
	}
	m := own.Mounts[best]
	kind, source := "bind", m.Source
	relative := strings.TrimPrefix(strings.TrimPrefix(directory, path.Clean(m.Destination)), "/")
	if relative == "" && m.Type == "volume" {
		kind, source = "volume", m.Name
	} else if relative != "" {
		source = path.Join(source, relative)
	}
	if source == "" || (m.Type != "bind" && m.Type != "volume") || strings.ContainsAny(source, ",\r\n") {
		return "", fmt.Errorf("unsupported provider mount at %s", directory)
	}
	mount := fmt.Sprintf("type=%s,source=%s,target=%s", kind, source, target)
	if readOnly {
		mount += ",readonly"
	}
	return mount, nil
}

func (p *ProviderContainers) network(ctx context.Context) (string, error) {
	network := strings.TrimSpace(os.Getenv("DOCKER_NETWORK"))
	if network == "" {
		network = "docktun"
	}
	if _, err := p.run(ctx, "network", "inspect", network); err != nil {
		if _, err := p.run(ctx, "network", "create", network); err != nil {
			return "", err
		}
	}
	return network, nil
}

func (p *ProviderContainers) tokenFile() string {
	return filepath.Join(filepath.Dir(p.cfg.CloudflaredConfig), "tunnel.token")
}

func (p *ProviderContainers) definition(ctx context.Context, provider string) ([]string, error) {
	network, err := p.network(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{"create", "--name", p.name(provider), "--restart", "unless-stopped", "--network", network, "--label", "docktun.provider=" + provider}
	switch provider {
	case "tailscale":
		volume := os.Getenv("TAILSCALE_STATE_VOLUME")
		if volume == "" {
			volume = "docktun-tailscale-state"
		}
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(volume) {
			return nil, fmt.Errorf("TAILSCALE_STATE_VOLUME must be a Docker volume name, not a container or host path")
		}
		if custom := strings.TrimSpace(os.Getenv("TAILSCALE_NETWORK")); custom != "" && custom != network {
			if _, err := p.run(ctx, "network", "inspect", custom); err != nil {
				return nil, fmt.Errorf("TAILSCALE_NETWORK: %w", err)
			}
			for i := range args {
				if i > 0 && args[i-1] == "--network" {
					args[i] = custom
					break
				}
			}
		}
		return append(args, "--hostname", "docktun", "--mount", "type=volume,source="+volume+",target=/var/lib/tailscale", "-e", "TS_USERSPACE=true", "-e", "TS_STATE_DIR=/var/lib/tailscale", "tailscale/tailscale:latest"), nil
	case "cloudflare", "wireguard":
		own, err := p.self(ctx)
		if err != nil {
			return nil, err
		}
		directory := filepath.Dir(p.cfg.CloudflaredConfig)
		target := directory
		if provider == "wireguard" {
			directory, target = filepath.Join(filepath.Dir(p.cfg.DBPath), "wireguard"), "/config"
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, err
		}
		mount, err := providerMount(own, directory, target, provider == "cloudflare")
		if err != nil {
			return nil, err
		}
		args = append(args, "--mount", mount)
		if provider == "wireguard" {
			port := strings.TrimSpace(os.Getenv("WIREGUARD_PORT"))
			if port == "" {
				port = "51820"
			}
			return append(args, "--cap-add", "NET_ADMIN", "-p", port+":51820/udp", "-e", "PUID=0", "-e", "PGID=0", "-e", "SERVERURL=auto", "-e", "SERVERPORT=51820", "-e", "PEERS=0", "-e", "PEERDNS=auto", "-e", "INTERNAL_SUBNET=10.13.13.0", "lscr.io/linuxserver/wireguard:latest"), nil
		}
		auth := "config"
		if data, err := os.ReadFile(p.tokenFile()); err == nil && strings.TrimSpace(string(data)) != "" {
			auth = "token"
		}
		args = append(args, "--label", "docktun.cloudflared.auth="+auth, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--add-host", "host.docker.internal:host-gateway", "cloudflare/cloudflared:latest", "tunnel", "--no-autoupdate", "--metrics", "0.0.0.0:2000")
		if auth == "token" {
			return append(args, "run", "--token-file", p.tokenFile()), nil
		}
		return append(args, "--config", p.cfg.CloudflaredConfig, "run"), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
}

func (p *ProviderContainers) ensure(ctx context.Context, provider string, refreshAuth bool) error {
	existing, err := p.inspect(ctx, p.name(provider))
	var args []string
	if err == nil {
		// Rebuild only a DockTun-created connector when explicitly switching auth.
		labels, _ := existing.Config["Labels"].(map[string]any)
		expected := "config"
		if token, _ := os.ReadFile(p.tokenFile()); len(strings.TrimSpace(string(token))) > 0 {
			expected = "token"
		}
		if provider != "cloudflare" || !refreshAuth || labels["docktun.provider"] != provider || labels["docktun.cloudflared.auth"] == expected {
			return nil
		}
		args, err = p.definition(ctx, provider)
		if err != nil {
			return err
		}
		if existing.State.Running {
			if _, err := p.run(ctx, "stop", p.name(provider)); err != nil {
				return err
			}
		}
		if _, err := p.run(ctx, "rm", p.name(provider)); err != nil {
			return err
		}
	} else if !strings.Contains(strings.ToLower(err.Error()), "no such") && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return err
	}
	if args == nil {
		args, err = p.definition(ctx, provider)
		if err != nil {
			return err
		}
	}
	_, err = p.run(ctx, args...)
	return err
}

func (p *ProviderContainers) Prepare(provider string) error {
	lock := providerLock(provider)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return p.ensure(ctx, provider, false)
}

func (p *ProviderContainers) Control(provider, action string) error {
	lock := providerLock(provider)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if action != "start" && action != "stop" && action != "restart" {
		return fmt.Errorf("invalid provider action")
	}
	if action != "stop" {
		if err := p.ensure(ctx, provider, true); err != nil {
			return err
		}
		if provider == "cloudflare" {
			if token, _ := os.ReadFile(p.tokenFile()); len(strings.TrimSpace(string(token))) == 0 {
				existing, _ := p.inspect(ctx, p.name(provider))
				labels, _ := existing.Config["Labels"].(map[string]any)
				if labels["docktun.provider"] == "cloudflare" {
					content, err := os.ReadFile(p.cfg.CloudflaredConfig)
					var settings struct {
						Tunnel      string `yaml:"tunnel"`
						Credentials string `yaml:"credentials-file"`
					}
					if err != nil || yaml.Unmarshal(content, &settings) != nil || settings.Tunnel == "" || settings.Credentials == "" {
						return fmt.Errorf("Cloudflare is stopped: save a connector token, or a YAML tunnel ID and credentials-file before enabling it")
					}
					if _, err := os.Stat(settings.Credentials); err != nil {
						return fmt.Errorf("Cloudflare credentials file is unavailable in DockTun: %s", settings.Credentials)
					}
				}
			}
		}
		// docker start is also valid for a newly created container. Restart works on
		// created containers too, but start avoids Docker-version-specific behavior.
		if c, err := p.inspect(ctx, p.name(provider)); err == nil && !c.State.Running {
			action = "start"
		}
	}
	_, err := p.run(ctx, action, p.name(provider))
	return err
}
