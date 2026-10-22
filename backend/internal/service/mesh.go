package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"docktun/internal/config"
	"gopkg.in/yaml.v3"
)

type PublicMeshConfig struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service,omitempty"`
}

type TailscaleConfig struct {
	AuthKey       string   `json:"auth_key,omitempty"`
	Hostname      string   `json:"hostname,omitempty"`
	AdvertiseTags []string `json:"advertise_tags,omitempty"`
	LoginServer   string   `json:"login_server,omitempty"`
}

type WireguardConfig struct {
	PrivateKey string          `json:"private_key,omitempty"`
	Interface  string          `json:"interface,omitempty"`
	ListenPort int             `json:"listen_port,omitempty"`
	Peers      []WireguardPeer `json:"peers,omitempty"`
}

type WireguardPeer struct {
	PublicKey  string `json:"public_key,omitempty"`
	AllowedIPs string `json:"allowed_ips,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
}

type MeshConfig struct {
	Mode       string           `json:"mode"`
	Enabled    bool             `json:"enabled"`
	TunnelName string           `json:"tunnel_name,omitempty"`
	TunnelID   string           `json:"tunnel_id,omitempty"`
	Public     PublicMeshConfig `json:"public,omitempty"`
	Tailscale  TailscaleConfig  `json:"tailscale,omitempty"`
	Wireguard  WireguardConfig  `json:"wireguard,omitempty"`
}

type MeshService struct {
	storagePath string
	configMgr   *ConfigManager
	appCfg      *config.Config
}

func NewMeshService(dataDir string, configMgr *ConfigManager, appCfg *config.Config) *MeshService {
	customPath := os.Getenv("DOCKTUN_MESH_CONFIG")
	if customPath == "" {
		customPath = filepath.Join(dataDir, "mesh.json")
	}
	return &MeshService{
		storagePath: customPath,
		configMgr:   configMgr,
		appCfg:      appCfg,
	}
}

func (s *MeshService) Load() (*MeshConfig, error) {
	var cfg MeshConfig
	data, err := os.ReadFile(s.storagePath)
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	if cfg.Mode == "" {
		cfg.Mode = "public"
	}

	// Auto-fill Cloudflare tunnel parameters from active tunnel configuration
	tunnelID, firstHost, firstSvc := s.getTunnelDefaults()

	if cfg.TunnelName == "" {
		if s.appCfg != nil && s.appCfg.TunnelName != "" {
			cfg.TunnelName = s.appCfg.TunnelName
		} else if envName := os.Getenv("TUNNEL_NAME"); envName != "" {
			cfg.TunnelName = envName
		} else if tunnelID != "" {
			cfg.TunnelName = tunnelID
		}
	}

	if cfg.TunnelID == "" && tunnelID != "" {
		cfg.TunnelID = tunnelID
	}

	if cfg.Public.Hostname == "" {
		if firstHost != "" {
			cfg.Public.Hostname = firstHost
		} else if envDom := os.Getenv("DOCKTUN_DOMAIN"); envDom != "" && envDom != "docktun.example.com" {
			cfg.Public.Hostname = envDom
		}
	}

	if cfg.Public.Service == "" {
		if firstSvc != "" {
			cfg.Public.Service = firstSvc
		} else {
			cfg.Public.Service = "http://docktun:3001"
		}
	}

	return &cfg, nil
}

func (s *MeshService) getTunnelDefaults() (tunnelID, hostname, service string) {
	if s.configMgr == nil {
		return
	}
	raw, err := s.configMgr.Read()
	if err != nil || raw == "" {
		return
	}

	var parsed struct {
		Tunnel  string `yaml:"tunnel"`
		Ingress []struct {
			Hostname string `yaml:"hostname"`
			Service  string `yaml:"service"`
		} `yaml:"ingress"`
	}
	if err := yaml.Unmarshal([]byte(raw), &parsed); err != nil {
		return
	}

	tunnelID = parsed.Tunnel
	for _, rule := range parsed.Ingress {
		if rule.Hostname != "" && rule.Service != "http_status:404" {
			hostname = rule.Hostname
			service = rule.Service
			break
		}
	}
	return
}

func (s *MeshService) Save(cfg *MeshConfig) error {
	if cfg == nil {
		return fmt.Errorf("mesh config is required")
	}

	if err := os.MkdirAll(filepath.Dir(s.storagePath), 0755); err != nil {
		return fmt.Errorf("create mesh dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mesh config: %w", err)
	}

	return os.WriteFile(s.storagePath, data, 0644)
}
