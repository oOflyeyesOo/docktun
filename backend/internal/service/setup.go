package service

import (
	"context"
	"encoding/json"
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

	// 1. Check if setup is already completed in system_settings
	var completedVal string
	_ = s.db.QueryRow(`SELECT value FROM system_settings WHERE key = 'setup_completed'`).Scan(&completedVal)

	// Check if users exist in SQLite
	var userCount int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount)
	status.HasAdminUser = userCount > 0

	// Determine completed state
	if completedVal == "true" && !force {
		status.Completed = true
	} else if userCount > 0 && s.cfg.AdminPassword != "" && completedVal == "" && !force {
		// Existing instance with predefined env password
		status.Completed = true
	} else {
		status.Completed = false
	}

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
	username := strings.TrimSpace(req.AdminUsername)
	if username == "" {
		username = "admin"
	}

	// 1. Set or update administrator account
	var adminID int
	if req.AdminPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), 12)
		if err != nil {
			return SetupCompleteResult{}, fmt.Errorf("failed to hash password: %w", err)
		}

		_, err = s.db.Exec(`
			INSERT INTO users (username, password_hash)
			VALUES (?, ?)
			ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash
		`, username, string(hash))
		if err != nil {
			return SetupCompleteResult{}, fmt.Errorf("failed to save admin user: %w", err)
		}
	}

	_ = s.db.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&adminID)
	if adminID == 0 {
		adminID = 1
	}

	// 2. Configure Tunnel / Remote Access
	tunnelStatus := "Local / mDNS Only"
	switch strings.ToLower(req.Tunnel.Type) {
	case "cloudflare":
		tunnelStatus = "Cloudflare Tunnel Configured"
		if req.Tunnel.CloudflareToken != "" {
			_, _ = s.db.Exec(`
				INSERT INTO system_settings (key, value) VALUES ('cloudflare_tunnel_token', ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
			`, strings.TrimSpace(req.Tunnel.CloudflareToken))
			tunnelStatus = "Cloudflare Token Tunnel Configured"
		}

		if req.Tunnel.CloudflareAPIToken != "" && s.cfAPI != nil {
			_ = s.cfAPI.SaveSettings(
				strings.TrimSpace(req.Tunnel.CloudflareAccountID),
				strings.TrimSpace(req.Tunnel.CloudflareAPIToken),
				strings.TrimSpace(req.Tunnel.CloudflareTunnelID),
				strings.TrimSpace(req.Tunnel.CloudflareTunnelName),
			)
			tunnelStatus = "Cloudflare API Managed Tunnel Connected"
		}

	case "wireguard":
		tunnelStatus = "WireGuard Point-to-Point Tunnel Activated"
		if req.Tunnel.WireguardServerURL != "" {
			_, _ = s.db.Exec(`
				INSERT INTO system_settings (key, value) VALUES ('wireguard_server_url', ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
			`, strings.TrimSpace(req.Tunnel.WireguardServerURL))
		}

	case "tailscale":
		tunnelStatus = "Tailscale Mesh Activated"
		if req.Tunnel.TailscaleAuthKey != "" {
			_, _ = s.db.Exec(`
				INSERT INTO system_settings (key, value) VALUES ('tailscale_auth_key', ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
			`, strings.TrimSpace(req.Tunnel.TailscaleAuthKey))
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

			// Connect to cloudflared network if requested
			if req.ImportContainers.ConnectNetwork {
				_ = s.dockerContainers.ConnectNetwork(cID, "cloudflared")
			}

			// Auto-register project stack if requested
			if req.ImportContainers.AutoImportStacks {
				if projID, err := s.dockerContainers.EnsureProjectForContainer(cID); err == nil && projID > 0 {
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
		_ = os.WriteFile(filepath.Join(s.dataDir, "notifications.json"), data, 0644)
	}

	// 5. Mark setup as completed in system_settings
	_, _ = s.db.Exec(`
		INSERT INTO system_settings (key, value) VALUES ('setup_completed', 'true')
		ON CONFLICT(key) DO UPDATE SET value = 'true', updated_at = CURRENT_TIMESTAMP
	`)

	if s.audit != nil {
		s.audit.Log(adminID, "setup_complete", fmt.Sprintf("Setup wizard completed via %s. Tunnel: %s, Imported: %d containers", ip, tunnelStatus, importedContainers), ip)
	}

	// 6. Generate immediate JWT auth tokens
	access, refresh := "", ""
	if tokenGen != nil {
		access, refresh, _ = tokenGen(adminID, username)
	}

	return SetupCompleteResult{
		OK:                 true,
		AccessToken:        access,
		RefreshToken:       refresh,
		Username:           username,
		ImportedContainers: importedContainers,
		ImportedStacks:     importedStacks,
		TunnelStatus:       tunnelStatus,
	}, nil
}
