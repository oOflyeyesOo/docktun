package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	mu               sync.Mutex
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

var (
	ErrSetupAlreadyConfigured = errors.New("setup is already configured; sign in to reconfigure")
	ErrSetupNotConfigured = errors.New("initial setup must be completed first")
	ErrWeakAdminPassword = errors.New("administrator password must be between 12 and 72 bytes")
)

// initialized checks both setup completion and accounts provisioned from the environment.
func (s *SetupService) initialized() (bool, error) {
	var initialized int
	err := s.db.QueryRow(`SELECT CASE WHEN EXISTS(SELECT 1 FROM users)
		OR EXISTS(SELECT 1 FROM system_settings WHERE key = 'setup_completed' AND value = 'true')
		THEN 1 ELSE 0 END`).Scan(&initialized)
	return initialized == 1, err
}

func (s *SetupService) GetStatus(force bool) (SetupStatus, error) {
	if !force {
		initialized, err := s.initialized()
		if err != nil {
			return SetupStatus{}, err
		}
		if initialized {
			// Do not leak Docker inventory, project paths, or tunnel state to unauthenticated callers.
			return SetupStatus{Completed: true, HasAdminUser: true}, nil
		}
	}
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

// Complete may only create the first administrator. The mutex prevents
// concurrent requests from both claiming initial setup.
func (s *SetupService) Complete(req SetupCompleteRequest, ip string, tokenGen func(int, string) (string, string, error)) (SetupCompleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	initialized, err := s.initialized()
	if err != nil {
		return SetupCompleteResult{}, err
	}
	if initialized {
		return SetupCompleteResult{}, ErrSetupAlreadyConfigured
	}
	if len(req.AdminPassword) < 12 || len(req.AdminPassword) > 72 {
		return SetupCompleteResult{}, ErrWeakAdminPassword
	}
	return s.applySetup(req, ip, tokenGen, 0)
}

// Reconfigure cannot modify administrator credentials; password changes
// belong in the authenticated endpoint that checks the existing password.
func (s *SetupService) Reconfigure(req SetupCompleteRequest, ip string, userID int) (SetupCompleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	initialized, err := s.initialized()
	if err != nil {
		return SetupCompleteResult{}, err
	}
	if !initialized {
		return SetupCompleteResult{}, ErrSetupNotConfigured
	}
	var username string
	if err := s.db.QueryRow(`SELECT username FROM users WHERE id = ?`, userID).Scan(&username); err != nil {
		return SetupCompleteResult{}, fmt.Errorf("authenticated administrator no longer exists: %w", err)
	}
	req.AdminUsername = username
	req.AdminPassword = ""
	return s.applySetup(req, ip, nil, userID)
}

func (s *SetupService) applySetup(req SetupCompleteRequest, ip string, tokenGen func(int, string) (string, string, error), existingAdminID int) (SetupCompleteResult, error) {
	username := strings.TrimSpace(req.AdminUsername)
	if username == "" {
		username = "admin"
	}

	adminID := existingAdminID
	if existingAdminID == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), 12)
		if err != nil {
			return SetupCompleteResult{}, fmt.Errorf("failed to hash password: %w", err)
		}
		result, err := s.db.Exec(`INSERT INTO users (username, password_hash) VALUES (?, ?)`, username, string(hash))
		if err != nil {
			return SetupCompleteResult{}, fmt.Errorf("failed to create admin user: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return SetupCompleteResult{}, err
		}
		adminID = int(id)
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
