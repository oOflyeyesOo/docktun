package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	ConfigurationSource string `json:"configuration_source"`
	RoutesKnown         bool   `json:"routes_known"`
	LastError           string `json:"last_error,omitempty"`
	Status              string `json:"status"`
	Running             bool   `json:"running"`
	TunnelID            string `json:"tunnel_id"`
	TunnelName          string `json:"tunnel_name"`
	IngressCount        int    `json:"ingress_count"`
	Protocol            string `json:"protocol"`
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
	LastError    string          `json:"last_error,omitempty"`
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

type ContainerExposureItem struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Image            string `json:"image"`
	Running          bool   `json:"running"`
	Ports            []int  `json:"ports"`
	ExposureMode     string `json:"exposure_mode"`
	PrivateType      string `json:"private_type"`
	PrivatePort      int    `json:"private_port"`
	Hostname         string `json:"hostname"`
	Target           string `json:"target"`
	TailscaleURL     string `json:"tailscale_url,omitempty"`
	WireguardURL     string `json:"wireguard_url,omitempty"`
	CloudflareURL    string `json:"cloudflare_url,omitempty"`
	ZeroTrustEnabled bool   `json:"zero_trust_enabled,omitempty"`
	ZeroTrustEmails  string `json:"zero_trust_emails,omitempty"`
}

type SetContainerExposureRequest struct {
	ContainerName     string `json:"container_name"`
	Mode              string `json:"mode"`         // "public", "private", "internal"
	PrivateType       string `json:"private_type"` // "tailscale", "wireguard"
	PrivatePort       int    `json:"private_port"`
	Hostname          string `json:"hostname"`
	Target            string `json:"target"`
	ZeroTrustEnabled  bool   `json:"zero_trust_enabled"`
	ZeroTrustEmails   string `json:"zero_trust_emails"`
	ZeroTrustAuthType string `json:"zero_trust_auth_type"`
	ZeroTrustSession  string `json:"zero_trust_session"`
	PrivateHost       string `json:"private_host"`
}

type RemoteAccessService struct {
	cfg                 *config.Config
	configMgr           *ConfigManager
	cfdSvc              *Cloudflared
	cfAPISvc            *CloudflareAPI
	meshSvc             *MeshService
	audit               *AuditLogger
	dataDir             string
	dockerContainersSvc *DockerContainers
	runDocker           func(context.Context, ...string) ([]byte, error)
	overviewMu          sync.RWMutex
	overviewCache       *RemoteAccessOverview
	overviewTime        time.Time
}

func NewRemoteAccessService(
	cfg *config.Config,
	configMgr *ConfigManager,
	cfdSvc *Cloudflared,
	cfAPISvc *CloudflareAPI,
	meshSvc *MeshService,
	audit *AuditLogger,
	dataDir string,
	dockerContainersSvc *DockerContainers,
) *RemoteAccessService {
	return &RemoteAccessService{
		cfg:                 cfg,
		configMgr:           configMgr,
		cfdSvc:              cfdSvc,
		cfAPISvc:            cfAPISvc,
		meshSvc:             meshSvc,
		audit:               audit,
		dataDir:             dataDir,
		dockerContainersSvc: dockerContainersSvc,
	}
}

func (s *RemoteAccessService) docker(ctx context.Context, args ...string) ([]byte, error) {
	if s.runDocker != nil {
		return s.runDocker(ctx, args...)
	}
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
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
	out, err := s.docker(ctx, "inspect", "--format", "{{.Id}}", name)
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func (s *RemoteAccessService) InvalidateOverviewCache() {
	s.overviewMu.Lock()
	s.overviewCache = nil
	s.overviewMu.Unlock()
}

func (s *RemoteAccessService) GetOverview() (*RemoteAccessOverview, error) {
	s.overviewMu.RLock()
	if time.Since(s.overviewTime) < 6*time.Second && s.overviewCache != nil {
		cached := s.overviewCache
		s.overviewMu.RUnlock()
		return cached, nil
	}
	s.overviewMu.RUnlock()
	cloudflare := ProcessState{Status: "disabled"}
	if s.cfdSvc != nil {
		cloudflare = s.cfdSvc.Status()
	}
	overview := &RemoteAccessOverview{
		Cloudflare: CloudflareSummary{
			Running:    cloudflare.Running,
			Status:     cloudflare.Status,
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

	if s.configMgr != nil {
		identity, identityErr := s.configMgr.TunnelIdentity()
		overview.Cloudflare.ConfigurationSource = "local"
		overview.Cloudflare.RoutesKnown = identityErr == nil
		if identity.Remote {
			overview.Cloudflare.ConfigurationSource = "cloudflare"
			overview.Cloudflare.TunnelID = identity.ID
			overview.Cloudflare.RoutesKnown = false
			overview.Cloudflare.IngressCount = 0
			if identityErr != nil {
				overview.Cloudflare.LastError = identityErr.Error()
			} else if s.cfAPISvc != nil {
				configuration, err := s.cfAPISvc.TunnelConfiguration(identity)
				if err != nil {
					overview.Cloudflare.LastError = err.Error()
				} else {
					overview.Cloudflare.RoutesKnown = true
					if rules, ok := configuration["ingress"].([]any); ok {
						for _, item := range rules {
							if rule, ok := item.(map[string]any); ok && rule["hostname"] != nil && rule["hostname"] != "" {
								overview.Cloudflare.IngressCount++
							}
						}
					}
				}
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

	s.overviewMu.Lock()
	s.overviewCache = overview
	s.overviewTime = time.Now()
	s.overviewMu.Unlock()

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
		status.LastError = "Cannot query tailscaled. Check the Tailscale container logs and its /var/lib/tailscale state volume permissions."
		status.BackendState = "Starting"
		return status, nil
	}

	var rawDoc struct {
		Version      string   `json:"Version"`
		BackendState string   `json:"BackendState"`
		AuthURL      string   `json:"AuthURL"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Self         struct {
			PublicKey    string   `json:"PublicKey"`
			HostName     string   `json:"HostName"`
			DNSName      string   `json:"DNSName"`
			OS           string   `json:"OS"`
			Online       bool     `json:"Online"`
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
	if authKey == "" && s.meshSvc != nil {
		mesh, err := s.meshSvc.Load()
		if err != nil {
			return err
		}
		authKey = mesh.Tailscale.AuthKey
	}
	if !s.isContainerRunning("docktun-tailscale") {
		if err := s.TailscaleControl("start"); err != nil {
			return fmt.Errorf("failed to start tailscale container: %w", err)
		}
		time.Sleep(2 * time.Second)
	}

	if hostname == "" {
		hostname = "docktun"
	}

	args := []string{"exec", "docktun-tailscale", "tailscale", "up", "--reset", "--hostname", hostname, "--accept-routes"}
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
	if s.dockerContainersSvc != nil {
		s.dockerContainersSvc.exposureMu.Lock()
		defer s.dockerContainersSvc.exposureMu.Unlock()
		return s.dockerContainersSvc.waitPendingTailscaleRules("docktun-tailscale")
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

func (s *RemoteAccessService) TailscaleSetOptions(advertiseRoutes string, exitNode bool) error {
	if !s.isContainerRunning("docktun-tailscale") {
		return fmt.Errorf("tailscale container is not running")
	}

	args := []string{"exec", "docktun-tailscale", "tailscale", "set"}
	if advertiseRoutes != "" {
		args = append(args, "--advertise-routes="+advertiseRoutes)
	} else {
		args = append(args, "--advertise-routes=")
	}
	if exitNode {
		args = append(args, "--advertise-exit-node")
	} else {
		args = append(args, "--advertise-exit-node=false")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale set failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (s *RemoteAccessService) defaultNetwork() string {
	if net := strings.TrimSpace(os.Getenv("TAILSCALE_NETWORK")); net != "" {
		return net
	}
	if net := strings.TrimSpace(os.Getenv("DOCKER_NETWORK")); net != "" {
		return net
	}
	if err := exec.Command("docker", "network", "inspect", "docktun").Run(); err == nil {
		return "docktun"
	}
	if err := exec.Command("docker", "network", "inspect", "cloudflared").Run(); err == nil {
		return "cloudflared"
	}
	return "docktun"
}

func (s *RemoteAccessService) TailscaleControl(action string) error {
	action = strings.ToLower(action)
	if s.dockerContainersSvc != nil {
		s.dockerContainersSvc.exposureMu.Lock()
		defer s.dockerContainersSvc.exposureMu.Unlock()
	}
	p := NewProviderContainers(s.cfg)
	if s.runDocker != nil {
		p.run = s.runDocker
	}
	if err := p.Control("tailscale", action); err != nil {
		return err
	}
	if action != "stop" && s.dockerContainersSvc != nil {
		if err := s.dockerContainersSvc.waitPendingTailscaleRules("docktun-tailscale"); err != nil {
			if errors.Is(err, errTailscaleNeedsConnection) {
				return nil // Preserve cleanup until interactive login completes.
			}
			_ = p.Control("tailscale", "stop")
			return err
		}
	}
	return nil
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
	p := NewProviderContainers(s.cfg)
	if s.runDocker != nil {
		p.run = s.runDocker
	}
	return p.Control("wireguard", strings.ToLower(action))
}

func (s *RemoteAccessService) WireguardAddPeer(name, allowedIPsMode string) (*WireguardPeerDetail, error) {
	cleanName := strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(name, "_"))
	if cleanName == "" {
		cleanName = fmt.Sprintf("peer_%d", time.Now().Unix())
	}
	if !strings.HasPrefix(cleanName, "peer_") {
		cleanName = "peer_" + cleanName
	}

	wgDir := filepath.Join(s.dataDir, "wireguard")
	peerDir := filepath.Join(wgDir, cleanName)
	_ = os.MkdirAll(peerDir, 0755)

	// 1. Generate keypair inside wireguard container or fallback
	var privKey, pubKey string
	if s.isContainerRunning("docktun-wireguard") {
		outPriv, err := exec.Command("docker", "exec", "docktun-wireguard", "wg", "genkey").Output()
		if err == nil {
			privKey = strings.TrimSpace(string(outPriv))
			cmdPub := exec.Command("docker", "exec", "-i", "docktun-wireguard", "wg", "pubkey")
			cmdPub.Stdin = bytes.NewReader([]byte(privKey))
			outPub, errPub := cmdPub.Output()
			if errPub == nil {
				pubKey = strings.TrimSpace(string(outPub))
			}
		}
	}

	if privKey == "" || pubKey == "" {
		// Fallback placeholder
		privKey = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("priv-%d", time.Now().UnixNano())))
		pubKey = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("pub-%d", time.Now().UnixNano())))
	}

	// 2. Determine client IP in 10.13.13.X
	currentPeers, _ := s.GetWireguardStatus()
	nextIP := 2
	if currentPeers != nil {
		nextIP = len(currentPeers.Peers) + 2
	}
	clientIP := fmt.Sprintf("10.13.13.%d/32", nextIP)

	// 3. Determine AllowedIPs (split tunnel with containers or full tunnel)
	allowedIPs := "10.13.13.0/24, 172.17.0.0/16, 172.18.0.0/16"
	if allowedIPsMode == "all" {
		allowedIPs = "0.0.0.0/0, ::/0"
	}

	serverPubKey := "SERVER_PUBLIC_KEY"
	serverPort := 51820
	if currentPeers != nil {
		if currentPeers.PublicKey != "" {
			serverPubKey = currentPeers.PublicKey
		}
		if currentPeers.Port > 0 {
			serverPort = currentPeers.Port
		}
	}

	confContent := fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s
DNS = 1.1.1.1, 8.8.8.8

[Peer]
PublicKey = %s
Endpoint = auto:%d
AllowedIPs = %s
PersistentKeepalive = 25
`, privKey, clientIP, serverPubKey, serverPort, allowedIPs)

	confFile := filepath.Join(peerDir, cleanName+".conf")
	_ = os.WriteFile(confFile, []byte(confContent), 0644)

	// 4. Live add to running container
	if s.isContainerRunning("docktun-wireguard") {
		_ = exec.Command("docker", "exec", "docktun-wireguard", "wg", "set", "wg0", "peer", pubKey, "allowed-ips", clientIP).Run()
	}

	// 5. Generate QR Code image if possible
	pngFile := filepath.Join(peerDir, cleanName+".png")
	if s.isContainerRunning("docktun-wireguard") {
		_ = exec.Command("docker", "exec", "-i", "docktun-wireguard", "qrencode", "-t", "png", "-o", fmt.Sprintf("/config/%s/%s.png", cleanName, cleanName), confContent).Run()
	}

	detail := &WireguardPeerDetail{
		Name:       strings.ToUpper(cleanName[:1]) + cleanName[1:],
		ConfigText: confContent,
		AllowedIPs: clientIP,
		PublicKey:  pubKey,
	}

	if pngBytes, err := os.ReadFile(pngFile); err == nil {
		detail.QRCodeDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	}

	return detail, nil
}

func (s *RemoteAccessService) WireguardDeletePeer(name string) error {
	cleanName := strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(name, "_"))
	if !strings.HasPrefix(cleanName, "peer_") {
		cleanName = "peer_" + cleanName
	}

	peerDir := filepath.Join(s.dataDir, "wireguard", cleanName)
	_ = os.RemoveAll(peerDir)
	return nil
}

func (s *RemoteAccessService) ZeroconfControl(action string) error {
	switch strings.ToLower(action) {
	case "start":
		if s.isContainerInstalled("docktun-zeroconf") {
			if err := exec.Command("docker", "start", "docktun-zeroconf").Run(); err == nil {
				return nil
			}
		}
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		cmdPrebuilt := exec.Command("docker", "run", "-d",
			"--name", "docktun-zeroconf",
			"--restart", "unless-stopped",
			"--network", "host",
			"-v", "/var/run/docker.sock:/var/run/docker.sock:ro",
			"-e", "DOMAIN=local",
			"-e", "HTTP_PORT=80",
			"-e", "DOCKTUN_PORT="+port,
			"--entrypoint", "/app/zeroconf",
			"ghcr.io/ooflyeyesoo/docktun:latest",
		)
		if err := cmdPrebuilt.Run(); err == nil {
			return nil
		}
		cmdLocal := exec.Command("docker", "run", "-d",
			"--name", "docktun-zeroconf",
			"--restart", "unless-stopped",
			"--network", "host",
			"-v", "/var/run/docker.sock:/var/run/docker.sock:ro",
			"-e", "DOMAIN=local",
			"-e", "HTTP_PORT=80",
			"-e", "DOCKTUN_PORT="+port,
			"docktun-zeroconf:latest",
		)
		if err := cmdLocal.Run(); err == nil {
			return nil
		}
		return exec.Command("docker", "compose", "up", "-d", "zeroconf").Run()
	case "stop":
		return exec.Command("docker", "stop", "docktun-zeroconf").Run()
	case "restart":
		if err := exec.Command("docker", "restart", "docktun-zeroconf").Run(); err == nil {
			return nil
		}
		return s.ZeroconfControl("start")
	case "recreate", "build":
		_ = exec.Command("docker", "rm", "-f", "docktun-zeroconf").Run()
		return s.ZeroconfControl("start")
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

// ==============================================================================
// Container Exposure Methods (Tailscale, WireGuard, Cloudflare)
// ==============================================================================

func (s *RemoteAccessService) GetContainerExposures() ([]ContainerExposureItem, error) {
	if s.dockerContainersSvc == nil {
		return []ContainerExposureItem{}, nil
	}

	containers, err := s.dockerContainersSvc.List()
	if err != nil {
		return nil, err
	}

	items := make([]ContainerExposureItem, 0, len(containers))
	for _, c := range containers {
		// Extract port numbers from port string (e.g. "80/tcp, 5432/tcp")
		ports := parsePortsFromString(c.Ports)

		item := ContainerExposureItem{
			ID:               c.ID,
			Name:             c.Name,
			Image:            c.Image,
			Running:          c.Running,
			Ports:            ports,
			ExposureMode:     c.ExposureMode,
			PrivateType:      c.PrivateType,
			PrivatePort:      c.PrivatePort,
			Hostname:         c.Hostname,
			Target:           c.Target,
			ZeroTrustEnabled: c.ZeroTrustEnabled,
			ZeroTrustEmails:  c.ZeroTrustEmails,
		}

		if c.ExposureMode == "public" && c.Hostname != "" {
			item.CloudflareURL = "https://" + c.Hostname
		}

		if c.ExposureMode == "private" {
			targetPort := c.PrivatePort
			if targetPort <= 0 && len(ports) > 0 {
				targetPort = ports[0]
			}
			if targetPort <= 0 {
				targetPort = 80
			}

			if c.PrivateType == "tailscale" {
				if c.PrivateHost != "" {
					item.TailscaleURL = "http://" + net.JoinHostPort(c.PrivateHost, strconv.Itoa(targetPort))
				}
			} else if c.PrivateType == "wireguard" {
				if c.PrivateHost != "" {
					item.WireguardURL = fmt.Sprintf("http://%s:%d", c.PrivateHost, targetPort)
				}
			}
		}

		items = append(items, item)
	}

	return items, nil
}

func (s *RemoteAccessService) SetContainerExposure(req SetContainerExposureRequest) error {
	if s.dockerContainersSvc == nil {
		return fmt.Errorf("container service not initialized")
	}

	settings := ExposureSettings{
		Mode:              req.Mode,
		Hostname:          req.Hostname,
		Target:            req.Target,
		PrivateType:       req.PrivateType,
		PrivateHost:       req.PrivateHost,
		PrivatePort:       req.PrivatePort,
		ZeroTrustEnabled:  req.ZeroTrustEnabled,
		ZeroTrustEmails:   req.ZeroTrustEmails,
		ZeroTrustAuthType: req.ZeroTrustAuthType,
		ZeroTrustSession:  req.ZeroTrustSession,
	}

	if req.Mode == "public" {
		if req.Target == "" {
			settings.Target = fmt.Sprintf("http://%s:%d", req.ContainerName, req.PrivatePort)
		}
	} else if req.Mode == "private" {
		if req.PrivateType == "" {
			return fmt.Errorf("choose a private access provider")
		}
		if req.PrivatePort <= 0 {
			settings.PrivatePort = 80
		}
	}

	return s.dockerContainersSvc.UpdateExposure(req.ContainerName, settings)
}

func parsePortsFromString(portStr string) []int {
	seen := make(map[int]bool)
	var ports []int

	re := regexp.MustCompile(`(\d+)/(tcp|udp)`)
	matches := re.FindAllStringSubmatch(portStr, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			if p, err := strconv.Atoi(m[1]); err == nil && !seen[p] {
				seen[p] = true
				ports = append(ports, p)
			}
		}
	}

	// Fallback to simple number parsing if format is e.g. "8080"
	if len(ports) == 0 {
		for _, part := range strings.Split(portStr, ",") {
			part = strings.TrimSpace(part)
			if p, err := strconv.Atoi(part); err == nil && !seen[p] {
				seen[p] = true
				ports = append(ports, p)
			}
		}
	}

	return ports
}
