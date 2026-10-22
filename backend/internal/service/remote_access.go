package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"docktun/internal/config"
	"gopkg.in/yaml.v3"
)

type RemoteAccessOverview struct {
	Cloudflare CloudflareSummary `json:"cloudflare"`
	Tailscale  TailscaleSummary  `json:"tailscale"`
	Wireguard  WireguardSummary  `json:"wireguard"`
	Zeroconf   ZeroconfSummary   `json:"zeroconf"`
}

type CloudflareSummary struct {
	Running      bool   `json:"running"`
	TunnelID     string `json:"tunnel_id"`
	TunnelName   string `json:"tunnel_name"`
	IngressCount int    `json:"ingress_count"`
	Protocol     string `json:"protocol"`
}

type TailscaleSummary struct {
	Installed    bool     `json:"installed"`
	Running      bool     `json:"running"`
	BackendState string   `json:"backend_state"`
	TailscaleIPs []string `json:"tailscale_ips"`
	MagicDNS     string   `json:"magic_dns"`
	PeersCount   int      `json:"peers_count"`
}

type WireguardSummary struct {
	Installed  bool   `json:"installed"`
	Running    bool   `json:"running"`
	Interface  string `json:"interface"`
	Port       int    `json:"port"`
	PeersCount int    `json:"peers_count"`
	ServerIP   string `json:"server_ip"`
}

type ZeroconfSummary struct {
	Running bool   `json:"running"`
	Domain  string `json:"domain"`
}

type TailscalePeer struct {
	ID           string   `json:"id"`
	HostName     string   `json:"hostname"`
	DNSName      string   `json:"dns_name"`
	TailscaleIPs []string `json:"tailscale_ips"`
	OS           string   `json:"os"`
	Online       bool     `json:"online"`
	LastSeen     string   `json:"last_seen,omitempty"`
}

type TailscaleStatus struct {
	Installed    bool            `json:"installed"`
	Running      bool            `json:"running"`
	Version      string          `json:"version"`
	BackendState string          `json:"backend_state"`
	AuthURL      string          `json:"auth_url,omitempty"`
	NodeKey      string          `json:"node_key,omitempty"`
	HostName     string          `json:"hostname"`
	DNSName      string          `json:"dns_name"`
	TailscaleIPs []string        `json:"tailscale_ips"`
	Peers        []TailscalePeer `json:"peers"`
}

type WireguardPeerDetail struct {
	Name            string `json:"name"`
	ConfigText      string `json:"config_text,omitempty"`
	QRCodeDataURL   string `json:"qr_code_data_url,omitempty"`
	AllowedIPs      string `json:"allowed_ips"`
	LatestHandshake string `json:"latest_handshake,omitempty"`
	TransferRx      string `json:"transfer_rx,omitempty"`
	TransferTx      string `json:"transfer_tx,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	PublicKey       string `json:"public_key,omitempty"`
}

type WireguardStatus struct {
	Installed bool                  `json:"installed"`
	Running   bool                  `json:"running"`
	Interface string                `json:"interface"`
	Port      int                   `json:"port"`
	PublicKey string                `json:"public_key"`
	ServerURL string                `json:"server_url"`
	Peers     []WireguardPeerDetail `json:"peers"`
}

type RemoteAccessService struct {
	cfg       *config.Config
	configMgr *ConfigManager
	cfdSvc    *Cloudflared
	cfAPISvc  *CloudflareAPI
	meshSvc   *MeshService
	audit     *AuditLogger
	dataDir   string
}

func NewRemoteAccessService(
	cfg *config.Config,
	configMgr *ConfigManager,
	cfdSvc *Cloudflared,
	cfAPISvc *CloudflareAPI,
	meshSvc *MeshService,
	audit *AuditLogger,
	dataDir string,
) *RemoteAccessService {
	return &RemoteAccessService{
		cfg:       cfg,
		configMgr: configMgr,
		cfdSvc:    cfdSvc,
		cfAPISvc:  cfAPISvc,
		meshSvc:   meshSvc,
		audit:     audit,
		dataDir:   dataDir,
	}
}

func (s *RemoteAccessService) isContainerRunning(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", name)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func (s *RemoteAccessService) isContainerInstalled(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Id}}", name)
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func (s *RemoteAccessService) GetOverview() (*RemoteAccessOverview, error) {
	overview := &RemoteAccessOverview{
		Cloudflare: CloudflareSummary{
			Running:    s.cfdSvc != nil && s.cfdSvc.IsRunning(),
			Protocol:   "http2",
			TunnelName: s.cfg.TunnelName,
		},
		Tailscale: TailscaleSummary{
			Installed:    s.isContainerInstalled("docktun-tailscale"),
			Running:      s.isContainerRunning("docktun-tailscale"),
			BackendState: "Stopped",
		},
		Wireguard: WireguardSummary{
			Installed:  s.isContainerInstalled("docktun-wireguard"),
			Running:    s.isContainerRunning("docktun-wireguard"),
			Interface:  "wg0",
			Port:       51820,
			PeersCount: 0,
		},
		Zeroconf: ZeroconfSummary{
			Running: s.isContainerRunning("docktun-zeroconf"),
			Domain:  "local",
		},
	}

	// Cloudflare metrics & ingress count
	if s.configMgr != nil {
		raw, err := s.configMgr.Read()
		if err == nil && raw != "" {
			var parsed struct {
				Tunnel   string `yaml:"tunnel"`
				Protocol string `yaml:"protocol"`
				Ingress  []struct {
					Hostname string `yaml:"hostname"`
					Service  string `yaml:"service"`
				} `yaml:"ingress"`
			}
			if yaml.Unmarshal([]byte(raw), &parsed) == nil {
				overview.Cloudflare.TunnelID = parsed.Tunnel
				if parsed.Protocol != "" {
					overview.Cloudflare.Protocol = parsed.Protocol
				}
				rulesCount := 0
				for _, r := range parsed.Ingress {
					if r.Service != "http_status:404" {
						rulesCount++
					}
				}
				overview.Cloudflare.IngressCount = rulesCount
			}
		}
	}

	// Tailscale summary
	if overview.Tailscale.Running {
		tsStatus, _ := s.GetTailscaleStatus()
		if tsStatus != nil {
			overview.Tailscale.BackendState = tsStatus.BackendState
			overview.Tailscale.TailscaleIPs = tsStatus.TailscaleIPs
			overview.Tailscale.MagicDNS = tsStatus.DNSName
			overview.Tailscale.PeersCount = len(tsStatus.Peers)
		}
	} else if !overview.Tailscale.Installed {
		overview.Tailscale.BackendState = "Not Installed"
	}

	// Wireguard summary
	if overview.Wireguard.Installed {
		wgStatus, _ := s.GetWireguardStatus()
		if wgStatus != nil {
			overview.Wireguard.PeersCount = len(wgStatus.Peers)
			overview.Wireguard.Port = wgStatus.Port
		}
	}

	return overview, nil
}

// ==============================================================================
// Tailscale Methods
// ==============================================================================

func (s *RemoteAccessService) GetTailscaleStatus() (*TailscaleStatus, error) {
	status := &TailscaleStatus{
		Installed:    s.isContainerInstalled("docktun-tailscale"),
		Running:      s.isContainerRunning("docktun-tailscale"),
		BackendState: "Stopped",
	}

	if !status.Installed {
		status.BackendState = "Not Installed"
		return status, nil
	}

	if !status.Running {
		return status, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "exec", "docktun-tailscale", "tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		status.BackendState = "Starting"
		return status, nil
	}

	var rawDoc struct {
		Version      string `json:"Version"`
		BackendState string `json:"BackendState"`
		AuthURL      string `json:"AuthURL"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Self         struct {
			PublicKey string   `json:"PublicKey"`
			HostName  string   `json:"HostName"`
			DNSName   string   `json:"DNSName"`
			OS        string   `json:"OS"`
			Online    bool     `json:"Online"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
		Peer map[string]struct {
			ID           string   `json:"ID"`
			HostName     string   `json:"HostName"`
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			OS           string   `json:"OS"`
			Online       bool     `json:"Online"`
			LastSeen     string   `json:"LastSeen"`
		} `json:"Peer"`
	}

	if err := json.Unmarshal(out, &rawDoc); err == nil {
		status.Version = rawDoc.Version
		status.BackendState = rawDoc.BackendState
		status.AuthURL = rawDoc.AuthURL
		status.HostName = rawDoc.Self.HostName
		status.DNSName = strings.TrimSuffix(rawDoc.Self.DNSName, ".")
		if len(rawDoc.Self.TailscaleIPs) > 0 {
			status.TailscaleIPs = rawDoc.Self.TailscaleIPs
		} else {
			status.TailscaleIPs = rawDoc.TailscaleIPs
		}

		for _, p := range rawDoc.Peer {
			status.Peers = append(status.Peers, TailscalePeer{
				ID:           p.ID,
				HostName:     p.HostName,
				DNSName:      strings.TrimSuffix(p.DNSName, "."),
				TailscaleIPs: p.TailscaleIPs,
				OS:           p.OS,
				Online:       p.Online,
				LastSeen:     p.LastSeen,
			})
		}
	}

	return status, nil
}

func (s *RemoteAccessService) TailscaleUp(authKey, hostname string) error {
	if !s.isContainerRunning("docktun-tailscale") {
		if err := s.TailscaleControl("start"); err != nil {
			return fmt.Errorf("failed to start tailscale container: %w", err)
		}
		time.Sleep(2 * time.Second)
	}

	if hostname == "" {
		hostname = "docktun"
	}

	args := []string{"exec", "docktun-tailscale", "tailscale", "up", "--hostname", hostname, "--accept-routes"}
	if authKey != "" {
		args = append(args, "--authkey", authKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale up failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (s *RemoteAccessService) TailscaleDown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", "docktun-tailscale", "tailscale", "down")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale down failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (s *RemoteAccessService) TailscaleControl(action string) error {
	switch strings.ToLower(action) {
	case "start":
		if s.isContainerInstalled("docktun-tailscale") {
			return exec.Command("docker", "start", "docktun-tailscale").Run()
		}
		// Launch via docker run
		dataDir := filepath.Join(s.dataDir, "tailscale")
		_ = os.MkdirAll(dataDir, 0755)
		runArgs := []string{
			"run", "-d",
			"--name", "docktun-tailscale",
			"--hostname", "docktun",
			"--restart", "unless-stopped",
			"--cap-add", "NET_ADMIN",
			"--cap-add", "NET_RAW",
			"--network", "cloudflared",
			"-v", fmt.Sprintf("%s:/var/lib/tailscale", dataDir),
			"-e", "TS_USERSPACE=true",
			"-e", "TS_STATE_DIR=/var/lib/tailscale",
			"tailscale/tailscale:latest",
		}
		out, err := exec.Command("docker", runArgs...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to launch tailscale container: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	case "stop":
		return exec.Command("docker", "stop", "docktun-tailscale").Run()

	case "restart":
		return exec.Command("docker", "restart", "docktun-tailscale").Run()

	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

// ==============================================================================
// WireGuard Methods
// ==============================================================================

func (s *RemoteAccessService) GetWireguardStatus() (*WireguardStatus, error) {
	status := &WireguardStatus{
		Installed: s.isContainerInstalled("docktun-wireguard"),
		Running:   s.isContainerRunning("docktun-wireguard"),
		Interface: "wg0",
		Port:      51820,
	}

	wgDir := filepath.Join(s.dataDir, "wireguard")

	// 1. Live wg show output
	if status.Running {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "exec", "docktun-wireguard", "wg", "show")
		if out, err := cmd.Output(); err == nil {
			text := string(out)
			lines := strings.Split(text, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "public key:") {
					status.PublicKey = strings.TrimSpace(strings.TrimPrefix(line, "public key:"))
				}
				if strings.HasPrefix(line, "listening port:") {
					if p, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "listening port:"))); err == nil {
						status.Port = p
					}
				}
			}
		}
	}

	// 2. Discover client peers generated in wireguard config folder
	peers := make([]WireguardPeerDetail, 0)
	if _, err := os.Stat(wgDir); err == nil {
		entries, err := os.ReadDir(wgDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), "peer") {
					pName := entry.Name()
					pDir := filepath.Join(wgDir, pName)
					confFile := filepath.Join(pDir, pName+".conf")
					pngFile := filepath.Join(pDir, pName+".png")

					peer := WireguardPeerDetail{
						Name: strings.ToUpper(pName[:1]) + pName[1:],
					}

					// Read client config
					if confBytes, err := os.ReadFile(confFile); err == nil {
						peer.ConfigText = string(confBytes)
						// Parse Address
						for _, cLine := range strings.Split(peer.ConfigText, "\n") {
							cLine = strings.TrimSpace(cLine)
							if strings.HasPrefix(cLine, "Address") && strings.Contains(cLine, "=") {
								parts := strings.SplitN(cLine, "=", 2)
								peer.AllowedIPs = strings.TrimSpace(parts[1])
							}
						}
					}

					// Read QR Code PNG
					if pngBytes, err := os.ReadFile(pngFile); err == nil {
						peer.QRCodeDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
					}

					peers = append(peers, peer)
				}
			}
		}
	}

	// If no peer folders exist yet but container is running, parse wg show peers directly
	if len(peers) == 0 && status.Running {
		cmd := exec.Command("docker", "exec", "docktun-wireguard", "wg", "show", "dump")
		if out, err := cmd.Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for i, line := range lines {
				if i == 0 {
					continue // skip interface line
				}
				fields := strings.Fields(line)
				if len(fields) >= 4 {
					peer := WireguardPeerDetail{
						Name:       fmt.Sprintf("Peer %d", i),
						PublicKey:  fields[0],
						Endpoint:   fields[2],
						AllowedIPs: fields[3],
					}
					if len(fields) >= 5 && fields[4] != "0" {
						if ts, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
							peer.LatestHandshake = time.Unix(ts, 0).Format("15:04:05 (Jan 02)")
						}
					}
					peers = append(peers, peer)
				}
			}
		}
	}

	status.Peers = peers
	return status, nil
}

func (s *RemoteAccessService) WireguardControl(action string) error {
	switch strings.ToLower(action) {
	case "start":
		if s.isContainerInstalled("docktun-wireguard") {
			return exec.Command("docker", "start", "docktun-wireguard").Run()
		}
		// Launch via docker run
		wgDir := filepath.Join(s.dataDir, "wireguard")
		_ = os.MkdirAll(wgDir, 0755)
		runArgs := []string{
			"run", "-d",
			"--name", "docktun-wireguard",
			"--restart", "unless-stopped",
			"--cap-add", "NET_ADMIN",
			"--cap-add", "SYS_MODULE",
			"--network", "cloudflared",
			"-p", "51820:51820/udp",
			"-v", fmt.Sprintf("%s:/config", wgDir),
			"-e", "PUID=1000",
			"-e", "PGID=1000",
			"-e", "TZ=UTC",
			"-e", "SERVERURL=auto",
			"-e", "SERVERPORT=51820",
			"-e", "PEERS=3",
			"-e", "PEERDNS=auto",
			"-e", "INTERNAL_SUBNET=10.13.13.0",
			"linuxserver/wireguard:latest",
		}
		out, err := exec.Command("docker", runArgs...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to launch wireguard container: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	case "stop":
		return exec.Command("docker", "stop", "docktun-wireguard").Run()

	case "restart":
		return exec.Command("docker", "restart", "docktun-wireguard").Run()

	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func (s *RemoteAccessService) ZeroconfControl(action string) error {
	switch strings.ToLower(action) {
	case "start":
		if s.isContainerInstalled("docktun-zeroconf") {
			return exec.Command("docker", "start", "docktun-zeroconf").Run()
		}
		return exec.Command("docker", "compose", "--profile", "zeroconf", "up", "-d").Run()
	case "stop":
		return exec.Command("docker", "stop", "docktun-zeroconf").Run()
	case "restart":
		return exec.Command("docker", "restart", "docktun-zeroconf").Run()
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}
