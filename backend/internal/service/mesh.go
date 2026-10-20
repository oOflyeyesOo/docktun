package service

import (
  "fmt"
  "os"
  "strings"
)

type MeshMode string

const (
  MeshModeNone     MeshMode = "none"
  MeshModeTailscale MeshMode = "tailscale"
  MeshModeWireguard MeshMode = "wireguard"
)

type MeshConfig struct {
  Mode       MeshMode `json:"mode"`
  Enabled    bool     `json:"enabled"`
  Tailscale  TailscaleConfig `json:"tailscale,omitempty"`
  Wireguard  WireguardConfig `json:"wireguard,omitempty"`
  TunnelName string   `json:"tunnel_name,omitempty"`
}

type TailscaleConfig struct {
  AuthKey       string `json:"auth_key,omitempty"`
  Hostname      string `json:"hostname,omitempty"`
  AdvertiseTags []string `json:"advertise_tags,omitempty"`
  LoginServer   string `json:"login_server,omitempty"`
}

type WireguardConfig struct {
  PrivateKey string `json:"private_key,omitempty"`
  Interface  string `json:"interface,omitempty"`
  ListenPort int    `json:"listen_port,omitempty"`
  Peers      []WireguardPeer `json:"peers,omitempty"`
}

type WireguardPeer struct {
  PublicKey string `json:"public_key,omitempty"`
  AllowedIPs string `json:"allowed_ips,omitempty"`
  Endpoint string `json:"endpoint,omitempty"`
}

type MeshService struct{}

func NewMeshService() *MeshService { return &MeshService{} }

func (s *MeshService) Load() (*MeshConfig, error) {
  cfgPath := os.Getenv("TUNLIFY_MESH_CONFIG")
  if cfgPath == "" {
    return &MeshConfig{Mode: MeshModeNone, Enabled: false}, nil
  }

  data, err := os.ReadFile(cfgPath)
  if err != nil {
    return nil, fmt.Errorf("read mesh config: %w", err)
  }

  cfg := &MeshConfig{Mode: MeshModeNone, Enabled: false}
  content := string(data)
  if strings.Contains(content, "tailscale") || strings.Contains(content, "auth_key") {
    cfg.Mode = MeshModeTailscale
    cfg.Enabled = true
  } else if strings.Contains(content, "wireguard") || strings.Contains(content, "private_key") {
    cfg.Mode = MeshModeWireguard
    cfg.Enabled = true
  }

  return cfg, nil
}

func (s *MeshService) Save(cfg *MeshConfig) error {
  if cfg == nil {
    return fmt.Errorf("mesh config is required")
  }

  if cfg.Mode == MeshModeNone { cfg.Enabled = false; return nil }

  path := os.Getenv("TUNLIFY_MESH_CONFIG")
  if path == "" {
    path = "/etc/tunlify/mesh.json"
  }

  if err := os.MkdirAll(strings.TrimSuffix(path, "/mesh.json"), 0755); err != nil {
    return fmt.Errorf("create mesh dir: %w", err)
  }

  payload := []byte("{}")
  if cfg.Mode == MeshModeTailscale {
    payload = []byte(fmt.Sprintf(`{"mode":"tailscale","enabled":true,"tailscale":{"auth_key":"%s","hostname":"%s","login_server":"%s"}}`, cfg.Tailscale.AuthKey, cfg.Tailscale.Hostname, cfg.Tailscale.LoginServer))
  }
  if cfg.Mode == MeshModeWireguard {
    payload = []byte(fmt.Sprintf(`{"mode":"wireguard","enabled":true,"wireguard":{"private_key":"%s","interface":"%s","listen_port":%d}}`, cfg.Wireguard.PrivateKey, cfg.Wireguard.Interface, cfg.Wireguard.ListenPort))
  }

  return os.WriteFile(path, payload, 0644)
}
