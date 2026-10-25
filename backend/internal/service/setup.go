package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"docktun/internal/config"
	"docktun/internal/db"
	"golang.org/x/crypto/bcrypt"
)

type DiscoveredContainer struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Image                  string `json:"image"`
	Status                 string `json:"status"`
	State                  string `json:"state"`
	Ports                  string `json:"ports"`
	Running                bool   `json:"running"`
	IsSystem               bool   `json:"is_system"`
	ConnectedToCloudflared bool   `json:"connected_to_cloudflared"`
	SuggestedStack         string `json:"suggested_stack"`
}

type DiscoveredStack struct {
	Name       string   `json:"name"`
	Path       string   `json:"path,omitempty"`
	Containers []string `json:"containers"`
}

type SetupStatus struct {
	Completed            bool                  `json:"completed"`
	HasAdminUser         bool                  `json:"has_admin_user"`
	AdminUsername        string                `json:"admin_username"`
	HostOS               string                `json:"host_os"`
	HostArch             string                `json:"host_arch"`
	NumCPU               int                   `json:"num_cpu"`
	DockerVersion        string                `json:"docker_version"`
	DockerConnected      bool                  `json:"docker_connected"`
	TunnelConfigured     bool                  `json:"tunnel_configured"`
	TunnelType           string                `json:"tunnel_type"`
	HasCloudflareToken   bool                  `json:"has_cloudflare_token"`
	DiscoveredContainers []DiscoveredContainer `json:"discovered_containers"`
	DiscoveredStacks     []DiscoveredStack     `json:"discovered_stacks"`
}

type SetupTunnelRequest struct {
	Type                 string `json:"type"` // "cloudflare", "wireguard", "tailscale", "none"
	CloudflareToken      string `json:"cloudflare_token,omitempty"`
	CloudflareAPIToken   string `json:"cloudflare_api_token,omitempty"`
	CloudflareAccountID  string `json:"cloudflare_account_id,omitempty"`
	CloudflareTunnelID   string `json:"cloudflare_tunnel_id,omitempty"`
	CloudflareTunnelName string `json:"cloudflare_tunnel_name,omitempty"`
	WireguardServerURL   string `json:"wireguard_server_url,omitempty"`
	WireguardPort        int    `json:"wireguard_port,omitempty"`
	TailscaleAuthKey     string `json:"tailscale_auth_key,omitempty"`
}

type SetupImportRequest struct {
	AutoImportStacks bool     `json:"auto_import_stacks"`
	ConnectNetwork   bool     `json:"connect_network"`
	ContainerIDs     []string `json:"container_ids"`
}

type SetupNotificationsRequest struct {
	Enabled    bool   `json:"enabled"`
	Type       string `json:"type"`
	WebhookURL string `json:"webhook_url"`
}

type SetupCompleteRequest struct {
	SetupToken       string                    `json:"setup_token"`
	AdminUsername    string                    `json:"admin_username"`
	AdminPassword    string                    `json:"admin_password"`
	Tunnel           SetupTunnelRequest        `json:"tunnel"`
	ImportContainers SetupImportRequest        `json:"import_containers"`
	Notifications    SetupNotificationsRequest `json:"notifications"`
}

type SetupCompleteResult struct {
	OK                 bool   `json:"ok"`
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token"`
	Username           string `json:"username"`
	ImportedContainers int    `json:"imported_containers"`
	ImportedStacks     int    `json:"imported_stacks"`
	TunnelStatus       string `json:"tunnel_status"`
}

type SetupService struct {
	cfg              *config.Config
	db               *db.DB
	dockerContainers *DockerContainers
	cfAPI            *CloudflareAPI
	configMgr        *ConfigManager
	meshSvc          *MeshService
	audit            *AuditLogger
	dataDir          string
}

func NewSetupService(
	cfg *config.Config,
	database *db.DB,
	dockerContainers *DockerContainers,
	cfAPI *CloudflareAPI,
	configMgr *ConfigManager,
	meshSvc *MeshService,
	audit *AuditLogger,
	dataDir string,
) *SetupService {
	return &SetupService{
		cfg:              cfg,
		db:               database,
		dockerContainers: dockerContainers,
		cfAPI:            cfAPI,
		configMgr:        configMgr,
		meshSvc:          meshSvc,
		audit:            audit,
		dataDir:          dataDir,
	}
}

func (s *SetupService) GetStatus(force bool) (SetupStatus, error) {
	status := SetupStatus{
		HostOS:        runtime.GOOS,
		HostArch:      runtime.GOARCH,
		NumCPU:        runtime.NumCPU(),
		AdminUsername: s.cfg.AdminUsername,
	}
	if status.AdminUsername == "" {
		status.AdminUsername = "admin"
	}

	locked, err := s.Locked()
	if err != nil {
		return SetupStatus{}, err
	}
	status.HasAdminUser = locked
	status.Completed = locked

	// 2. Check Docker availability & version
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if out, err := cmd.Output(); err == nil {
		status.DockerConnected = true
		status.DockerVersion = strings.TrimSpace(string(out))
		if status.DockerVersion == "" {
			status.DockerVersion = "Active"
		}
	} else {
		status.DockerConnected = false
		status.DockerVersion = "Docker Daemon Unavailable"
	}

	// 3. Check existing tunnel config
	if _, err := os.Stat(s.cfg.CloudflaredConfig); err == nil {
		status.TunnelConfigured = true
		status.TunnelType = "cloudflare"
	} else {
		var cfTok string
		_ = s.db.QueryRow(`SELECT value FROM system_settings WHERE key = 'cloudflare_tunnel_token'`).Scan(&cfTok)
		if cfTok != "" {
			status.TunnelConfigured = true
			status.TunnelType = "cloudflare"
			status.HasCloudflareToken = true
		} else {
			status.TunnelType = "none"
		}
	}

	// Persisted selection takes precedence over leftover Cloudflare files/tokens.
	if s.meshSvc != nil {
		mesh, err := s.meshSvc.Load()
		if err != nil {
			return SetupStatus{}, err
		}
		status.TunnelType = mesh.Mode
		if mesh.Mode == "public" {
			status.TunnelType = "cloudflare"
		}
		status.TunnelConfigured = mesh.Mode != "none"
	}

	// 4. Discover existing containers & stacks
	if status.DockerConnected && s.dockerContainers != nil {
		if cList, err := s.dockerContainers.List(); err == nil {
			stackMap := make(map[string][]string)
			discovered := make([]DiscoveredContainer, 0, len(cList))

			for _, c := range cList {
				lowerName := strings.ToLower(c.Name)
				isCore := strings.HasPrefix(lowerName, "docktun") ||
					lowerName == "cloudflared" ||
					lowerName == "wireguard" ||
					lowerName == "tailscale"

				sugStack := c.ProjectName
				if sugStack == "" {
					sugStack = c.Name
				}

				dc := DiscoveredContainer{
					ID:                     c.ID,
					Name:                   c.Name,
					Image:                  c.Image,
					Status:                 c.Status,
					State:                  c.State,
					Ports:                  c.Ports,
					Running:                c.Running,
					IsSystem:               isCore,
					ConnectedToCloudflared: c.ConnectedToCloudflared,
					SuggestedStack:         sugStack,
				}
				discovered = append(discovered, dc)

				if !isCore && sugStack != "" {
					stackMap[sugStack] = append(stackMap[sugStack], c.Name)
				}
			}

			status.DiscoveredContainers = discovered
			for sName, cNames := range stackMap {
				status.DiscoveredStacks = append(status.DiscoveredStacks, DiscoveredStack{
					Name:       sName,
					Containers: cNames,
				})
			}
		}
	}

	return status, nil
}

func (s *SetupService) Complete(req SetupCompleteRequest, ip string, tokenGen func(int, string) (string, string, error)) (SetupCompleteResult, error) {
	locked, err := s.Locked()
	if err != nil {
		return SetupCompleteResult{}, err
	}
	if locked {
		return SetupCompleteResult{}, ErrSetupLocked
	}
	if len(s.cfg.SetupToken) < 32 || subtle.ConstantTimeCompare([]byte(req.SetupToken), []byte(s.cfg.SetupToken)) != 1 {
		return SetupCompleteResult{}, ErrSetupToken
	}
	username := strings.TrimSpace(req.AdminUsername)
	if username == "" {
		username = "admin"
	}
	if len(username) > 128 || len(req.AdminPassword) < 12 || len(req.AdminPassword) > 72 {
		return SetupCompleteResult{}, ErrSetupInput
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), 12)
	if err != nil {
		return SetupCompleteResult{}, err
	}
	// A single conditional write closes the check/create race across processes too.
	result, err := s.db.Exec(`INSERT INTO users (username, password_hash)
 SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM users)
 AND NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'setup_completed' AND value = 'true')`, username, string(hash))
	if err != nil {
		return SetupCompleteResult{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return SetupCompleteResult{}, err
	}
	if n != 1 {
		return SetupCompleteResult{}, ErrSetupLocked
	}
	id, err := result.LastInsertId()
	if err != nil {
		return SetupCompleteResult{}, err
	}
	adminID := int(id)
	access, refresh := "", ""
	if tokenGen != nil {
		access, refresh, err = tokenGen(adminID, username)
		if err != nil {
			return SetupCompleteResult{}, fmt.Errorf("account created; sign in to continue: %w", err)
		}
	}
	resultData, err := s.applyConfiguration(req, adminID, ip)
	if err != nil {
		return SetupCompleteResult{}, err
	}
	resultData.AccessToken, resultData.RefreshToken, resultData.Username = access, refresh, username
	return resultData, nil
}

var ErrSetupToken = errors.New("invalid setup token; read setup.token on the host")
var ErrSetupLocked = errors.New("setup is locked; sign in to reconfigure")
var ErrSetupInput = errors.New("use a username up to 128 bytes and a password between 12 and 72 bytes")
var ErrSetupIdentity = errors.New("use account settings to change credentials")

// Locked fails closed when the database cannot establish the bootstrap state.
func (s *SetupService) Locked() (bool, error) {
	var locked bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users) OR EXISTS(SELECT 1 FROM system_settings WHERE key = 'setup_completed' AND value = 'true')`).Scan(&locked)
	return locked, err
}

// Reconfigure never creates accounts, changes passwords, or mints sessions.
func (s *SetupService) Reconfigure(req SetupCompleteRequest, userID int, ip string) (SetupCompleteResult, error) {
	var username string
	if userID <= 0 {
		return SetupCompleteResult{}, ErrSetupIdentity
	}
	if err := s.db.QueryRow(`SELECT username FROM users WHERE id = ?`, userID).Scan(&username); err != nil {
		return SetupCompleteResult{}, ErrSetupIdentity
	}
	if req.AdminPassword != "" || (req.AdminUsername != "" && req.AdminUsername != username) {
		return SetupCompleteResult{}, ErrSetupIdentity
	}
	return s.applyConfiguration(req, userID, ip)
}

func (s *SetupService) applyConfiguration(req SetupCompleteRequest, adminID int, ip string) (SetupCompleteResult, error) {
	provider := strings.ToLower(strings.TrimSpace(req.Tunnel.Type))
	if provider == "" {
		provider = "none"
	}
	switch provider {
	case "cloudflare", "tailscale", "wireguard", "none":
	default:
		return SetupCompleteResult{}, fmt.Errorf("unsupported tunnel provider: %s", provider)
	}
	if s.meshSvc != nil {
		mesh, err := s.meshSvc.Load()
		if err != nil {
			return SetupCompleteResult{}, err
		}
		mesh.Mode, mesh.Enabled = provider, provider != "none"
		if provider == "cloudflare" {
			mesh.Mode = "public"
		}
		if provider == "tailscale" && req.Tunnel.TailscaleAuthKey != "" {
			mesh.Tailscale.AuthKey = strings.TrimSpace(req.Tunnel.TailscaleAuthKey)
		}
		if err := s.meshSvc.Save(mesh); err != nil {
			return SetupCompleteResult{}, err
		}
	}
	// 2. Configure Tunnel / Remote Access
	tunnelStatus := "Local / mDNS Only"
	switch provider {
	case "cloudflare":
		tunnelStatus = "Cloudflare Tunnel Configured"
		if req.Tunnel.CloudflareToken != "" {
			_, err := s.db.Exec(`
				INSERT INTO system_settings (key, value) VALUES ('cloudflare_tunnel_token', ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
			`, strings.TrimSpace(req.Tunnel.CloudflareToken))
			if err != nil {
				return SetupCompleteResult{}, err
			}
			tunnelStatus = "Cloudflare Token Tunnel Configured"
		}

		if req.Tunnel.CloudflareAPIToken != "" && s.cfAPI != nil {
			err := s.cfAPI.SaveSettings(
				strings.TrimSpace(req.Tunnel.CloudflareAccountID),
				strings.TrimSpace(req.Tunnel.CloudflareAPIToken),
				strings.TrimSpace(req.Tunnel.CloudflareTunnelID),
				strings.TrimSpace(req.Tunnel.CloudflareTunnelName),
			)
			if err != nil {
				return SetupCompleteResult{}, err
			}
			tunnelStatus = "Cloudflare API Managed Tunnel Connected"
		}

	case "wireguard":
		tunnelStatus = "WireGuard settings saved"
		if req.Tunnel.WireguardServerURL != "" {
			_, err := s.db.Exec(`
				INSERT INTO system_settings (key, value) VALUES ('wireguard_server_url', ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
			`, strings.TrimSpace(req.Tunnel.WireguardServerURL))
			if err != nil {
				return SetupCompleteResult{}, err
			}
		}

	case "tailscale":
		tunnelStatus = "Tailscale settings saved"
		if req.Tunnel.TailscaleAuthKey != "" {
			_, err := s.db.Exec(`
				INSERT INTO system_settings (key, value) VALUES ('tailscale_auth_key', ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
			`, strings.TrimSpace(req.Tunnel.TailscaleAuthKey))
			if err != nil {
				return SetupCompleteResult{}, err
			}
		}
	}

	// 3. Import Selected Containers & Stacks
	importedContainers := 0
	importedStacks := 0
	if len(req.ImportContainers.ContainerIDs) > 0 && s.dockerContainers != nil {
		for _, cID := range req.ImportContainers.ContainerIDs {
			cID = strings.TrimSpace(cID)
			if cID == "" {
				continue
			}

			// Connect to DockTun network if requested
			if req.ImportContainers.ConnectNetwork {
				if err := s.dockerContainers.ConnectNetwork(cID, ""); err != nil {
					return SetupCompleteResult{}, err
				}
			}

			// Auto-register project stack if requested
			if req.ImportContainers.AutoImportStacks {
				projID, err := s.dockerContainers.EnsureProjectForContainer(cID)
				if err != nil {
					return SetupCompleteResult{}, err
				}
				if projID > 0 {
					importedStacks++
				}
			}
			importedContainers++
		}
	}

	// 4. Configure Default Notifications if provided
	if req.Notifications.Enabled && req.Notifications.WebhookURL != "" {
		notifCfg := map[string]interface{}{
			"enabled":     true,
			"type":        req.Notifications.Type,
			"webhook_url": req.Notifications.WebhookURL,
		}
		data, _ := json.MarshalIndent(notifCfg, "", "  ")
		if err := os.WriteFile(filepath.Join(s.dataDir, "notifications.json"), data, 0600); err != nil {
			return SetupCompleteResult{}, err
		}
	}

	// 5. Mark setup as completed in system_settings
	_, err := s.db.Exec(`
		INSERT INTO system_settings (key, value) VALUES ('setup_completed', 'true')
		ON CONFLICT(key) DO UPDATE SET value = 'true', updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return SetupCompleteResult{}, err
	}

	if s.audit != nil {
		s.audit.Log(adminID, "setup_complete", fmt.Sprintf("Setup wizard completed via %s. Tunnel: %s, Imported: %d containers", ip, tunnelStatus, importedContainers), ip)
	}

	return SetupCompleteResult{
		OK:                 true,
		ImportedContainers: importedContainers,
		ImportedStacks:     importedStacks,
		TunnelStatus:       tunnelStatus,
	}, nil
}
