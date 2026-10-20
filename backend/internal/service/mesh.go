package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Public     PublicMeshConfig `json:"public,omitempty"`
	Tailscale  TailscaleConfig  `json:"tailscale,omitempty"`
	Wireguard  WireguardConfig  `json:"wireguard,omitempty"`
}

type MeshService struct {
	storagePath string
}

func NewMeshService(dataDir string) *MeshService {
	customPath := os.Getenv("TUNLIFY_MESH_CONFIG")
	if customPath == "" {
		customPath = filepath.Join(dataDir, "mesh.json")
	}
	return &MeshService{storagePath: customPath}
}

func (s *MeshService) Load() (*MeshConfig, error) {
	data, err := os.ReadFile(s.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &MeshConfig{Mode: "public", Enabled: false}, nil
		}
		return nil, fmt.Errorf("read mesh config: %w", err)
	}

	var cfg MeshConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &MeshConfig{Mode: "public", Enabled: false}, nil
	}
	if cfg.Mode == "" {
		cfg.Mode = "public"
	}
	return &cfg, nil
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
