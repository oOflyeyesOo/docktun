package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"docktun/internal/config"
	"docktun/internal/db"
	"gopkg.in/yaml.v3"
)

type ExposureSettings struct {
	Mode              string `json:"mode"`
	Hostname          string `json:"hostname,omitempty"`
	Target            string `json:"target,omitempty"`
	PrivateType       string `json:"private_type,omitempty"`
	PrivateHost       string `json:"private_host,omitempty"`
	PrivatePort       int    `json:"private_port,omitempty"`
	Notes             string `json:"notes,omitempty"`
	ZeroTrustEnabled  bool   `json:"zero_trust_enabled,omitempty"`
	ZeroTrustEmails   string `json:"zero_trust_emails,omitempty"`
	ZeroTrustAuthType string `json:"zero_trust_auth_type,omitempty"`
	ZeroTrustSession  string `json:"zero_trust_session,omitempty"`
	ZeroTrustAppID    string `json:"zero_trust_app_id,omitempty"`
}

type ContainerMount struct {
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	RW          bool   `json:"rw"`
}

type Container struct {
	ID                     string           `json:"id"`
	Name                   string           `json:"name"`
	Image                  string           `json:"image"`
	Status                 string           `json:"status"`
	State                  string           `json:"state"`
	Ports                  string           `json:"ports"`
	Command                string           `json:"command"`
	Created                string           `json:"created"`
	Running                bool             `json:"running"`
	ExposureMode           string           `json:"exposure_mode"`
	Hostname               string           `json:"hostname,omitempty"`
	Target                 string           `json:"target,omitempty"`
	PrivateType            string           `json:"private_type,omitempty"`
	PrivateHost            string           `json:"private_host,omitempty"`
	PrivatePort            int              `json:"private_port,omitempty"`
	Notes                  string           `json:"notes,omitempty"`
	ZeroTrustEnabled       bool             `json:"zero_trust_enabled,omitempty"`
	ZeroTrustEmails        string           `json:"zero_trust_emails,omitempty"`
	ZeroTrustAuthType      string           `json:"zero_trust_auth_type,omitempty"`
	ZeroTrustSession       string           `json:"zero_trust_session,omitempty"`
	CPU                    string           `json:"cpu,omitempty"`
	Memory                 string           `json:"memory,omitempty"`
	Network                string           `json:"network,omitempty"`
	BlockIO                string           `json:"block_io,omitempty"`
	PIDs                   int              `json:"pids,omitempty"`
	RestartCount           int              `json:"restart_count,omitempty"`
	Uptime                 string           `json:"uptime,omitempty"`
	Exposure               ExposureSettings `json:"exposure,omitempty"`
	ProjectID              int              `json:"project_id,omitempty"`
	ProjectName            string           `json:"project_name,omitempty"`
	ProjectPath            string           `json:"project_path,omitempty"`
	ConnectedToCloudflared bool             `json:"connected_to_cloudflared"`
	UpdateAvailable        bool             `json:"update_available"`
	LatestDigest           string           `json:"latest_digest,omitempty"`
	CurrentDigest          string           `json:"current_digest,omitempty"`
	UpdateCheckedAt        string           `json:"update_checked_at,omitempty"`
	UpdateError            string           `json:"update_error,omitempty"`
}

type ContainerDetail struct {
	Container
	ProjectID              int                 `json:"project_id,omitempty"`
	ProjectName            string              `json:"project_name,omitempty"`
	ProjectPath            string              `json:"project_path,omitempty"`
	ComposeFile            string              `json:"compose_file,omitempty"`
	Env                    []string            `json:"env,omitempty"`
	RawEnv                 []string            `json:"-"`
	Mounts                 []ContainerMount    `json:"mounts,omitempty"`
	IPAddress              string              `json:"ip_address,omitempty"`
	Gateway                string              `json:"gateway,omitempty"`
	MacAddress             string              `json:"mac_address,omitempty"`
	Networks               []string            `json:"networks,omitempty"`
	ExitCode               int                 `json:"exit_code,omitempty"`
	ConnectedToCloudflared bool                `json:"connected_to_cloudflared"`
	User                   string              `json:"user,omitempty"`
	WorkingDir             string              `json:"working_dir,omitempty"`
	RestartPolicy          string              `json:"restart_policy,omitempty"`
	Privileged             bool                `json:"privileged,omitempty"`
	CapAdd                 []string            `json:"cap_add,omitempty"`
	Labels                 map[string]string   `json:"labels,omitempty"`
	NetworkAliases         map[string][]string `json:"network_aliases,omitempty"`
	Cmd                    []string            `json:"cmd,omitempty"`
}

type cfOriginRequest struct {
	NoTLSVerify    bool   `yaml:"noTLSVerify,omitempty"`
	HTTPHostHeader string `yaml:"httpHostHeader,omitempty"`
}

type cfIngressRule struct {
	Hostname      string           `yaml:"hostname,omitempty"`
	Service       string           `yaml:"service"`
	Path          string           `yaml:"path,omitempty"`
	OriginRequest *cfOriginRequest `yaml:"originRequest,omitempty"`
}

type cfConfigDoc struct {
	Tunnel          string          `yaml:"tunnel"`
	CredentialsFile string          `yaml:"credentials-file"`
	Protocol        string          `yaml:"protocol,omitempty"`
	Metrics         string          `yaml:"metrics,omitempty"`
	Ingress         []cfIngressRule `yaml:"ingress"`
}

type DockerContainers struct {
	cfg         *config.Config
	db          *db.DB
	configMgr   *ConfigManager
	cloudflared *Cloudflared
	cfAPI       *CloudflareAPI
	audit       *AuditLogger
	updateMu    sync.RWMutex
	statsMu     sync.RWMutex
	statsCache  map[string]liveStats
	statsTime   time.Time
}

func NewDockerContainers(cfg *config.Config, db *db.DB, configMgr *ConfigManager, cloudflared *Cloudflared, cfAPI *CloudflareAPI, audit *AuditLogger) *DockerContainers {
	return &DockerContainers{
		cfg:         cfg,
		db:          db,
		configMgr:   configMgr,
		cloudflared: cloudflared,
		cfAPI:       cfAPI,
		audit:       audit,
	}
}

type liveStats struct {
	cpu     string
	mem     string
	net     string
	block   string
	pids    int
}

type projRef struct {
	ID   int
	Name string
	Path string
}

func (s *DockerContainers) loadAllProjectsMap() (map[string]projRef, map[string]projRef) {
	byPath := make(map[string]projRef)
	byName := make(map[string]projRef)
	if s.db == nil {
		return byPath, byName
	}
	rows, err := s.db.Query(`SELECT id, name, path FROM projects`)
	if err != nil {
		return byPath, byName
	}
	defer rows.Close()
	for rows.Next() {
		var p projRef
		if err := rows.Scan(&p.ID, &p.Name, &p.Path); err == nil {
			if p.Path != "" {
				byPath[p.Path] = p
			}
			if p.Name != "" {
				byName[strings.ToLower(p.Name)] = p
			}
		}
	}
	return byPath, byName
}

func (s *DockerContainers) List() ([]Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.State}}\t{{.Ports}}\t{{.RunningFor}}\t{{.Command}}\t{{.Networks}}\t{{.Labels}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps failed: %w", err)
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return []Container{}, nil
	}

	statsMap := s.getCachedStats()
	ingressMap := s.loadCloudflareIngressMap()
	dbExposures := s.loadAllExposuresFromDB()
	projectsByPath, projectsByName := s.loadAllProjectsMap()
	cachedUpdates := s.loadAllUpdatesFromDB()

	rows := strings.Split(text, "\n")
	containers := make([]Container, 0, len(rows))

	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}

		fields := strings.SplitN(row, "\t", 10)
		if len(fields) < 6 {
			continue
		}

		c := Container{
			ID:      strings.TrimSpace(fields[0]),
			Name:    strings.TrimSpace(fields[1]),
			Image:   strings.TrimSpace(fields[2]),
			Status:  strings.TrimSpace(fields[3]),
			State:   strings.TrimSpace(fields[4]),
			Ports:   strings.TrimSpace(fields[5]),
			Running: strings.EqualFold(fields[4], "running"),
		}
		if len(fields) >= 7 {
			c.Uptime = strings.TrimSpace(fields[6])
		}
		if len(fields) >= 8 {
			c.Command = strings.TrimSpace(fields[7])
		}

		if c.Name == "" {
			c.Name = c.ID
		}
		c.Name = strings.TrimPrefix(c.Name, "/")
		c.Running = c.Running || strings.HasPrefix(strings.ToLower(c.Status), "up ")

		if len(fields) >= 9 {
			networksStr := strings.TrimSpace(fields[8])
			c.ConnectedToCloudflared = strings.Contains(networksStr, "docktun") || strings.Contains(networksStr, "cloudflared")
		}

		if len(fields) >= 10 {
			labelsStr := strings.TrimSpace(fields[9])
			var compProj, compDir string
			for _, part := range strings.Split(labelsStr, ",") {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) == 2 {
					k := strings.TrimSpace(kv[0])
					v := strings.TrimSpace(kv[1])
					if k == "com.docker.compose.project" {
						compProj = v
					} else if k == "com.docker.compose.project.working_dir" {
						compDir = v
					}
				}
			}
			if p, ok := projectsByPath[compDir]; ok {
				c.ProjectID = p.ID
				c.ProjectName = p.Name
				c.ProjectPath = p.Path
			} else if p, ok := projectsByName[strings.ToLower(compProj)]; ok {
				c.ProjectID = p.ID
				c.ProjectName = p.Name
				c.ProjectPath = p.Path
			} else if compProj != "" || compDir != "" {
				c.ProjectName = compProj
				c.ProjectPath = compDir
				if s.db != nil {
					pName := compProj
					if pName == "" {
						pName = filepath.Base(compDir)
					}
					pPath := compDir
					if pPath == "" {
						pPath = "/opt/projects/" + pName
					}
					_, _ = s.db.Exec(`INSERT OR IGNORE INTO projects (name, path) VALUES (?, ?)`, pName, pPath)
					var newID int
					if compDir != "" {
						_ = s.db.QueryRow(`SELECT id FROM projects WHERE path = ?`, compDir).Scan(&newID)
					}
					if newID == 0 {
						_ = s.db.QueryRow(`SELECT id FROM projects WHERE lower(name) = ?`, strings.ToLower(pName)).Scan(&newID)
					}
					if newID > 0 {
						c.ProjectID = newID
						c.ProjectName = pName
						c.ProjectPath = pPath
						projectsByName[strings.ToLower(pName)] = projRef{ID: newID, Name: pName, Path: pPath}
						if pPath != "" {
							projectsByPath[pPath] = projRef{ID: newID, Name: pName, Path: pPath}
						}
					}
				}
			}
		}
		if c.ProjectID == 0 {
			if p, ok := projectsByName[strings.ToLower(c.Name)]; ok {
				c.ProjectID = p.ID
				c.ProjectName = p.Name
				c.ProjectPath = p.Path
			} else {
				cLower := strings.ToLower(c.Name)
				for pNameLower, p := range projectsByName {
					if strings.HasPrefix(cLower, pNameLower+"-") || strings.HasPrefix(cLower, pNameLower+"_") {
						c.ProjectID = p.ID
						c.ProjectName = p.Name
						c.ProjectPath = p.Path
						break
					}
				}
			}
		}

		cLower := strings.ToLower(c.Name)
		isDocktunCore := strings.HasPrefix(cLower, "docktun") ||
			cLower == "cloudflared" ||
			cLower == "wireguard" ||
			cLower == "wg-easy" ||
			cLower == "tailscale" ||
			cLower == "zeroconf" ||
			strings.EqualFold(c.ProjectName, "docktun") ||
			strings.EqualFold(c.ProjectName, "tun")

		if isDocktunCore {
			c.ProjectName = "docktun"
			if p, ok := projectsByName["docktun"]; ok {
				c.ProjectID = p.ID
				c.ProjectPath = p.Path
			} else if p, ok := projectsByName["tun"]; ok {
				c.ProjectID = p.ID
				c.ProjectPath = p.Path
			}
		}

		if c.Running {
			if st, ok := statsMap[c.Name]; ok {
				c.CPU = st.cpu
				c.Memory = st.mem
				c.Network = st.net
				c.BlockIO = st.block
				c.PIDs = st.pids
			} else if st, ok := statsMap[c.ID]; ok {
				c.CPU = st.cpu
				c.Memory = st.mem
				c.Network = st.net
				c.BlockIO = st.block
				c.PIDs = st.pids
			} else if len(c.ID) >= 12 {
				if st, ok := statsMap[c.ID[:12]]; ok {
					c.CPU = st.cpu
					c.Memory = st.mem
					c.Network = st.net
					c.BlockIO = st.block
					c.PIDs = st.pids
				}
			}
		}

		// Determine exposure:
		// 1. Check if public in Cloudflare Tunnel ingress
		if ing, ok := ingressMap[c.Name]; ok {
			c.ExposureMode = "public"
			c.Hostname = ing.Hostname
			c.Target = ing.Service
			c.Exposure = ExposureSettings{
				Mode:     "public",
				Hostname: ing.Hostname,
				Target:   ing.Service,
			}
		} else if exp, ok := dbExposures[c.Name]; ok {
			c.ExposureMode = exp.Mode
			c.Hostname = exp.Hostname
			c.Target = exp.Target
			c.PrivateType = exp.PrivateType
			c.PrivateHost = exp.PrivateHost
			c.PrivatePort = exp.PrivatePort
			c.Notes = exp.Notes
			c.Exposure = exp
		} else {
			c.ExposureMode = "internal"
			c.Exposure = ExposureSettings{Mode: "internal"}
		}

		if c.Status == "" {
			c.Status = c.State
		}

		if u, ok := cachedUpdates[c.ID]; ok {
			c.UpdateAvailable = u.UpdateAvailable
			c.LatestDigest = u.LatestDigest
			c.CurrentDigest = u.CurrentDigest
			c.UpdateCheckedAt = u.CheckedAt
			c.UpdateError = u.Error
		} else if u, ok := cachedUpdates[c.Name]; ok {
			c.UpdateAvailable = u.UpdateAvailable
			c.LatestDigest = u.LatestDigest
			c.CurrentDigest = u.CurrentDigest
			c.UpdateCheckedAt = u.CheckedAt
			c.UpdateError = u.Error
		}

		containers = append(containers, c)
	}

	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Running != containers[j].Running {
			return containers[i].Running
		}
		return strings.ToLower(containers[i].Name) < strings.ToLower(containers[j].Name)
	})

	return containers, nil
}

func (s *DockerContainers) getCachedStats() map[string]liveStats {
	s.statsMu.RLock()
	if time.Since(s.statsTime) < 3500*time.Millisecond && len(s.statsCache) > 0 {
		cached := make(map[string]liveStats, len(s.statsCache))
		for k, v := range s.statsCache {
			cached[k] = v
		}
		s.statsMu.RUnlock()
		return cached
	}
	s.statsMu.RUnlock()

	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if time.Since(s.statsTime) < 3500*time.Millisecond && len(s.statsCache) > 0 {
		cached := make(map[string]liveStats, len(s.statsCache))
		for k, v := range s.statsCache {
			cached[k] = v
		}
		return cached
	}

	fresh := s.collectAllStats()
	s.statsCache = fresh
	s.statsTime = time.Now()
	return fresh
}

func (s *DockerContainers) collectAllStats() map[string]liveStats {
	statsMap := make(map[string]liveStats)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.ID}}\t{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.BlockIO}}\t{{.PIDs}}")
	out, err := cmd.Output()
	if err != nil {
		return statsMap
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		name := strings.TrimPrefix(strings.TrimSpace(parts[1]), "/")
		pids, _ := strconv.Atoi(strings.TrimSpace(parts[6]))
		st := liveStats{
			cpu:   strings.TrimSpace(parts[2]),
			mem:   strings.TrimSpace(parts[3]),
			net:   strings.TrimSpace(parts[4]),
			block: strings.TrimSpace(parts[5]),
			pids:  pids,
		}
		if name != "" {
			statsMap[name] = st
		}
		if id != "" {
			statsMap[id] = st
			if len(id) > 12 {
				statsMap[id[:12]] = st
			}
		}
	}
	return statsMap
}

func (s *DockerContainers) Details(id string) (ContainerDetail, error) {
	detail := ContainerDetail{}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .}}", id)
	out, err := cmd.Output()
	if err != nil {
		return detail, fmt.Errorf("docker inspect failed: %w", err)
	}

	var inspect struct {
		ID      string `json:"Id"`
		Name    string `json:"Name"`
		Created string `json:"Created"`
		State   struct {
			Status       string `json:"Status"`
			Running      bool   `json:"Running"`
			StartedAt    string `json:"StartedAt"`
			Restarting   bool   `json:"Restarting"`
			RestartCount int    `json:"RestartCount"`
			ExitCode     int    `json:"ExitCode"`
		} `json:"State"`
		Config struct {
			Image      string            `json:"Image"`
			Cmd        []string          `json:"Cmd"`
			Env        []string          `json:"Env"`
			User       string            `json:"User"`
			WorkingDir string            `json:"WorkingDir"`
			Labels     map[string]string `json:"Labels"`
		} `json:"Config"`
		HostConfig struct {
			RestartPolicy struct {
				Name              string `json:"Name"`
				MaximumRetryCount int    `json:"MaximumRetryCount"`
			} `json:"RestartPolicy"`
			Privileged bool     `json:"Privileged"`
			CapAdd     []string `json:"CapAdd"`
		} `json:"HostConfig"`
		Mounts []struct {
			Type        string `json:"Type"`
			Name        string `json:"Name"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
			Mode        string `json:"Mode"`
			RW          bool   `json:"RW"`
		} `json:"Mounts"`
		NetworkSettings struct {
			IPAddress  string `json:"IPAddress"`
			Gateway    string `json:"Gateway"`
			MacAddress string `json:"MacAddress"`
			Networks   map[string]struct {
				IPAddress string   `json:"IPAddress"`
				Aliases   []string `json:"Aliases"`
			} `json:"Networks"`
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}

	if err := json.Unmarshal(out, &inspect); err != nil {
		return detail, fmt.Errorf("inspect parse failed: %w", err)
	}

	c := Container{
		ID:           strings.TrimSpace(inspect.ID),
		Name:         strings.TrimPrefix(strings.TrimSpace(inspect.Name), "/"),
		Image:        strings.TrimSpace(inspect.Config.Image),
		Status:       strings.TrimSpace(inspect.State.Status),
		State:        strings.TrimSpace(inspect.State.Status),
		Created:      strings.TrimSpace(inspect.Created),
		Running:      inspect.State.Running,
		Command:      strings.Join(inspect.Config.Cmd, " "),
		RestartCount: inspect.State.RestartCount,
	}

	if c.Name == "" {
		c.Name = id
	}

	if c.Running {
		c.Uptime = uptimeFromStartedAt(inspect.State.StartedAt)
	}

	// Fetch live stats from cache or single container fallback
	statsMap := s.getCachedStats()
	foundStats := false
	if st, ok := statsMap[c.Name]; ok {
		c.CPU = st.cpu
		c.Memory = st.mem
		c.Network = st.net
		c.BlockIO = st.block
		c.PIDs = st.pids
		foundStats = true
	} else if st, ok := statsMap[c.ID]; ok {
		c.CPU = st.cpu
		c.Memory = st.mem
		c.Network = st.net
		c.BlockIO = st.block
		c.PIDs = st.pids
		foundStats = true
	} else if len(c.ID) >= 12 {
		if st, ok := statsMap[c.ID[:12]]; ok {
			c.CPU = st.cpu
			c.Memory = st.mem
			c.Network = st.net
			c.BlockIO = st.block
			c.PIDs = st.pids
			foundStats = true
		}
	}

	if !foundStats && c.Running {
		sCtx, sCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer sCancel()
		statsCmd := exec.CommandContext(sCtx, "docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.BlockIO}}\t{{.PIDs}}", id)
		if statsOut, statsErr := statsCmd.Output(); statsErr == nil {
			parts := strings.Split(strings.TrimSpace(string(statsOut)), "\t")
			if len(parts) >= 5 {
				c.CPU = strings.TrimSpace(parts[0])
				c.Memory = strings.TrimSpace(parts[1])
				c.Network = strings.TrimSpace(parts[2])
				c.BlockIO = strings.TrimSpace(parts[3])
				if p, err := strconv.Atoi(strings.TrimSpace(parts[4])); err == nil {
					c.PIDs = p
				}
			}
		}
	}

	// Formatted ports (standard Docker format: [HostIP:]HostPort->ContainerPort)
	ports := []string{}
	for port, bindings := range inspect.NetworkSettings.Ports {
		if len(bindings) == 0 {
			ports = append(ports, port)
			continue
		}
		for _, b := range bindings {
			if b.HostPort != "" {
				hostIP := strings.TrimSpace(b.HostIP)
				if hostIP == "" || hostIP == "0.0.0.0" || hostIP == "::" || hostIP == "[::]" || hostIP == ":::" {
					ports = append(ports, fmt.Sprintf("%s->%s", b.HostPort, port))
				} else {
					if strings.Contains(hostIP, ":") && !strings.HasPrefix(hostIP, "[") {
						hostIP = "[" + hostIP + "]"
					}
					ports = append(ports, fmt.Sprintf("%s:%s->%s", hostIP, b.HostPort, port))
				}
			} else {
				ports = append(ports, port)
			}
		}
	}
	if len(ports) > 0 {
		c.Ports = strings.Join(ports, ", ")
	}

	// Exposure check
	ingressMap := s.loadCloudflareIngressMap()
	if ing, ok := ingressMap[c.Name]; ok {
		exp := s.getExposureFromDB(c.Name)
		c.ExposureMode = "public"
		c.Hostname = ing.Hostname
		c.Target = ing.Service
		c.ZeroTrustEnabled = exp.ZeroTrustEnabled
		c.ZeroTrustEmails = exp.ZeroTrustEmails
		c.ZeroTrustAuthType = exp.ZeroTrustAuthType
		c.ZeroTrustSession = exp.ZeroTrustSession
		c.Exposure = ExposureSettings{
			Mode:              "public",
			Hostname:          ing.Hostname,
			Target:            ing.Service,
			ZeroTrustEnabled:  exp.ZeroTrustEnabled,
			ZeroTrustEmails:   exp.ZeroTrustEmails,
			ZeroTrustAuthType: exp.ZeroTrustAuthType,
			ZeroTrustSession:  exp.ZeroTrustSession,
			ZeroTrustAppID:    exp.ZeroTrustAppID,
		}
	} else {
		exp := s.getExposureFromDB(c.Name)
		c.ExposureMode = exp.Mode
		c.Hostname = exp.Hostname
		c.Target = exp.Target
		c.PrivateType = exp.PrivateType
		c.PrivateHost = exp.PrivateHost
		c.PrivatePort = exp.PrivatePort
		c.Notes = exp.Notes
		c.ZeroTrustEnabled = exp.ZeroTrustEnabled
		c.ZeroTrustEmails = exp.ZeroTrustEmails
		c.ZeroTrustAuthType = exp.ZeroTrustAuthType
		c.ZeroTrustSession = exp.ZeroTrustSession
		c.Exposure = exp
	}

	// Mounts
	mounts := make([]ContainerMount, 0, len(inspect.Mounts))
	for _, m := range inspect.Mounts {
		mounts = append(mounts, ContainerMount{
			Type:        m.Type,
			Name:        m.Name,
			Source:      m.Source,
			Destination: m.Destination,
			Mode:        m.Mode,
			RW:          m.RW,
		})
	}

	// Networks
	nets := make([]string, 0, len(inspect.NetworkSettings.Networks))
	for netName := range inspect.NetworkSettings.Networks {
		nets = append(nets, netName)
	}

	// Mask environment variables that look like secrets
	maskedEnv := make([]string, 0, len(inspect.Config.Env))
	for _, env := range inspect.Config.Env {
		kv := strings.SplitN(env, "=", 2)
		if len(kv) == 2 {
			kUpper := strings.ToUpper(kv[0])
			if strings.Contains(kUpper, "SECRET") || strings.Contains(kUpper, "PASSWORD") || strings.Contains(kUpper, "TOKEN") || strings.Contains(kUpper, "KEY") {
				maskedEnv = append(maskedEnv, fmt.Sprintf("%s=********", kv[0]))
				continue
			}
		}
		maskedEnv = append(maskedEnv, env)
	}

	detail.Container = c
	detail.Env = maskedEnv
	detail.RawEnv = inspect.Config.Env
	detail.Mounts = mounts
	detail.IPAddress = inspect.NetworkSettings.IPAddress
	detail.Gateway = inspect.NetworkSettings.Gateway
	detail.MacAddress = inspect.NetworkSettings.MacAddress
	detail.Networks = nets
	detail.ExitCode = inspect.State.ExitCode
	detail.User = inspect.Config.User
	detail.WorkingDir = inspect.Config.WorkingDir
	detail.Cmd = inspect.Config.Cmd
	detail.Labels = inspect.Config.Labels
	detail.Privileged = inspect.HostConfig.Privileged
	detail.CapAdd = inspect.HostConfig.CapAdd
	detail.RestartPolicy = inspect.HostConfig.RestartPolicy.Name
	if detail.RestartPolicy == "on-failure" && inspect.HostConfig.RestartPolicy.MaximumRetryCount > 0 {
		detail.RestartPolicy = fmt.Sprintf("on-failure:%d", inspect.HostConfig.RestartPolicy.MaximumRetryCount)
	}
	detail.NetworkAliases = make(map[string][]string)
	for netName, netInfo := range inspect.NetworkSettings.Networks {
		if len(netInfo.Aliases) > 0 {
			detail.NetworkAliases[netName] = netInfo.Aliases
		}
	}

	_, hasDocktun := inspect.NetworkSettings.Networks["docktun"]
	_, hasCloudflared := inspect.NetworkSettings.Networks["cloudflared"]
	detail.ConnectedToCloudflared = hasDocktun || hasCloudflared
	detail.Container.ConnectedToCloudflared = hasDocktun || hasCloudflared

	compProj := inspect.Config.Labels["com.docker.compose.project"]
	compDir := inspect.Config.Labels["com.docker.compose.project.working_dir"]
	compFile := inspect.Config.Labels["com.docker.compose.project.config_files"]
	detail.ComposeFile = compFile

	projectsByPath, projectsByName := s.loadAllProjectsMap()
	if p, ok := projectsByPath[compDir]; ok {
		detail.ProjectID = p.ID
		detail.ProjectName = p.Name
		detail.ProjectPath = p.Path
		detail.Container.ProjectID = p.ID
		detail.Container.ProjectName = p.Name
		detail.Container.ProjectPath = p.Path
	} else if p, ok := projectsByName[strings.ToLower(compProj)]; ok {
		detail.ProjectID = p.ID
		detail.ProjectName = p.Name
		detail.ProjectPath = p.Path
		detail.Container.ProjectID = p.ID
		detail.Container.ProjectName = p.Name
		detail.Container.ProjectPath = p.Path
	} else if p, ok := projectsByName[strings.ToLower(c.Name)]; ok {
		detail.ProjectID = p.ID
		detail.ProjectName = p.Name
		detail.ProjectPath = p.Path
		detail.Container.ProjectID = p.ID
		detail.Container.ProjectName = p.Name
		detail.Container.ProjectPath = p.Path
	} else {
		cLower := strings.ToLower(c.Name)
		for pNameLower, p := range projectsByName {
			if strings.HasPrefix(cLower, pNameLower+"-") || strings.HasPrefix(cLower, pNameLower+"_") {
				detail.ProjectID = p.ID
				detail.ProjectName = p.Name
				detail.ProjectPath = p.Path
				detail.Container.ProjectID = p.ID
				detail.Container.ProjectName = p.Name
				detail.Container.ProjectPath = p.Path
				break
			}
		}
	}

	// Auto-register project if working_dir exists on host disk
	if detail.ProjectID == 0 && compDir != "" && s.db != nil {
		pName := compProj
		if pName == "" {
			pName = filepath.Base(compDir)
		}
		_, _ = s.db.Exec(`INSERT OR IGNORE INTO projects (name, path) VALUES (?, ?)`, pName, compDir)
		var newID int
		_ = s.db.QueryRow(`SELECT id FROM projects WHERE path = ?`, compDir).Scan(&newID)
		if newID > 0 {
			detail.ProjectID = newID
			detail.ProjectName = pName
			detail.ProjectPath = compDir
			detail.Container.ProjectID = newID
			detail.Container.ProjectName = pName
			detail.Container.ProjectPath = compDir
		}
	}

	cLower := strings.ToLower(c.Name)
	isDocktunCore := strings.HasPrefix(cLower, "docktun") ||
		cLower == "cloudflared" ||
		cLower == "wireguard" ||
		cLower == "wg-easy" ||
		cLower == "tailscale" ||
		cLower == "zeroconf" ||
		strings.EqualFold(detail.ProjectName, "docktun") ||
		strings.EqualFold(detail.ProjectName, "tun")

	if isDocktunCore {
		detail.ProjectName = "docktun"
		detail.Container.ProjectName = "docktun"
		if p, ok := projectsByName["docktun"]; ok {
			detail.ProjectID = p.ID
			detail.ProjectPath = p.Path
			detail.Container.ProjectID = p.ID
			detail.Container.ProjectPath = p.Path
		} else if p, ok := projectsByName["tun"]; ok {
			detail.ProjectID = p.ID
			detail.ProjectPath = p.Path
			detail.Container.ProjectID = p.ID
			detail.Container.ProjectPath = p.Path
		}
	}

	if u, ok := s.getUpdateFromDB(detail.ID); ok {
		detail.UpdateAvailable = u.UpdateAvailable
		detail.LatestDigest = u.LatestDigest
		detail.CurrentDigest = u.CurrentDigest
		detail.UpdateCheckedAt = u.CheckedAt
		detail.UpdateError = u.Error
		detail.Container.UpdateAvailable = u.UpdateAvailable
		detail.Container.LatestDigest = u.LatestDigest
		detail.Container.CurrentDigest = u.CurrentDigest
		detail.Container.UpdateCheckedAt = u.CheckedAt
		detail.Container.UpdateError = u.Error
	} else if u, ok := s.getUpdateFromDB(detail.Name); ok {
		detail.UpdateAvailable = u.UpdateAvailable
		detail.LatestDigest = u.LatestDigest
		detail.CurrentDigest = u.CurrentDigest
		detail.UpdateCheckedAt = u.CheckedAt
		detail.UpdateError = u.Error
		detail.Container.UpdateAvailable = u.UpdateAvailable
		detail.Container.LatestDigest = u.LatestDigest
		detail.Container.CurrentDigest = u.CurrentDigest
		detail.Container.UpdateCheckedAt = u.CheckedAt
		detail.Container.UpdateError = u.Error
	}

	return detail, nil
}

func (s *DockerContainers) DefaultNetwork() string {
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

func (s *DockerContainers) isSelfContainer(id string, name string) bool {
	cname := strings.ToLower(strings.TrimPrefix(name, "/"))
	if cname == "docktun" {
		return true
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		if strings.HasPrefix(id, host) || strings.HasPrefix(host, id) {
			return true
		}
	}
	return false
}

func (s *DockerContainers) ReconcileNetworks() (map[string]interface{}, error) {
	result := make(map[string]interface{})
	var logs []string
	addLog := func(msg string) {
		log.Printf("[network-reconcile] %s", msg)
		logs = append(logs, msg)
	}

	defNet := s.DefaultNetwork()
	_ = s.EnsureNetwork(defNet)
	addLog(fmt.Sprintf("Primary network: %s", defNet))

	hasDocktunNet := exec.Command("docker", "network", "inspect", "docktun").Run() == nil
	hasCloudflaredNet := exec.Command("docker", "network", "inspect", "cloudflared").Run() == nil

	networksToBridge := []string{defNet}
	if hasDocktunNet && defNet != "docktun" {
		networksToBridge = append(networksToBridge, "docktun")
	}
	if hasCloudflaredNet && defNet != "cloudflared" {
		networksToBridge = append(networksToBridge, "cloudflared")
	}

	// 1. Identify DockTun core container and connect to all bridge networks
	docktunContainers := []string{"docktun"}
	if host, err := os.Hostname(); err == nil && host != "" {
		docktunContainers = append(docktunContainers, host)
	}

	for _, c := range docktunContainers {
		if exec.Command("docker", "inspect", c).Run() == nil {
			for _, net := range networksToBridge {
				if err := s.ConnectNetwork(c, net); err == nil {
					addLog(fmt.Sprintf("Connected DockTun container %s to network %s", c, net))
				}
			}
			break
		}
	}

	// 2. Bridge Cloudflared tunnel container
	cfdContainer := s.cfg.CloudflaredContainer
	if cfdContainer == "" {
		cfdContainer = "docktun-cloudflared"
	}
	cfdConnected := false
	if exec.Command("docker", "inspect", cfdContainer).Run() == nil {
		for _, net := range networksToBridge {
			if err := s.ConnectNetwork(cfdContainer, net); err == nil {
				addLog(fmt.Sprintf("Connected %s to network %s", cfdContainer, net))
				cfdConnected = true
			}
		}
	}

	// 3. Bridge Tailscale container if present
	tsContainer := "docktun-tailscale"
	if exec.Command("docker", "inspect", tsContainer).Run() == nil {
		for _, net := range networksToBridge {
			_ = s.ConnectNetwork(tsContainer, net)
		}
	}

	// 4. Ensure all exposed containers are connected to bridged networks
	if s.db != nil {
		rows, err := s.db.Query(`SELECT container_name FROM container_exposure WHERE mode IN ('public', 'private')`)
		if err == nil && rows != nil {
			defer rows.Close()
			for rows.Next() {
				var cName string
				if err := rows.Scan(&cName); err == nil && cName != "" {
					for _, net := range networksToBridge {
						_ = s.ConnectNetwork(cName, net)
					}
					addLog(fmt.Sprintf("Verified network connectivity for exposed container %s", cName))
				}
			}
		}
	}

	// 5. Refresh DNS by restarting cloudflared to flush DNS cache and re-establish connection pools
	if s.cloudflared != nil && s.cloudflared.IsRunning() {
		addLog("Restarting cloudflared to flush DNS cache and re-establish connection pools...")
		if err := s.cloudflared.Restart(); err != nil {
			addLog(fmt.Sprintf("Cloudflared restart notice: %v", err))
		} else {
			addLog("✔ Cloudflared successfully restarted with refreshed DNS!")
		}
	}

	// 6. Automatically sync/reconcile Cloudflare DNS records for all public ingress routes
	if s.cfAPI != nil && s.configMgr != nil {
		addLog("Re-verifying and syncing Cloudflare DNS routes for public services...")
		s.cfAPI.AutoSyncIngressDNS(s.configMgr)
		addLog("✔ Cloudflare DNS sync check dispatched")
	}

	result["success"] = true
	result["logs"] = logs
	result["networks"] = networksToBridge
	result["cloudflared_bridged"] = cfdConnected
	return result, nil
}

func (s *DockerContainers) EnsureNetwork(network string) error {
	if network == "" {
		network = s.DefaultNetwork()
	}
	cmd := exec.Command("docker", "network", "inspect", network)
	if err := cmd.Run(); err != nil {
		createCmd := exec.Command("docker", "network", "create", network)
		_ = createCmd.Run()
	}
	return nil
}

func (s *DockerContainers) ConnectNetwork(id string, network string) error {
	if network == "" {
		network = s.DefaultNetwork()
	}
	_ = s.EnsureNetwork(network)
	cmd := exec.Command("docker", "network", "connect", network, id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		if strings.Contains(outStr, "already exists in network") {
			return nil
		}
		return fmt.Errorf("connect network failed: %s", outStr)
	}
	return nil
}

func (s *DockerContainers) DisconnectNetwork(id string, network string) error {
	if network == "" {
		network = s.DefaultNetwork()
	}
	cmd := exec.Command("docker", "network", "disconnect", network, id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("disconnect network failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *DockerContainers) EnsureProjectForContainer(id string) (int, error) {
	detail, err := s.Details(id)
	if err != nil {
		return 0, err
	}

	cname := strings.TrimPrefix(detail.Name, "/")
	if cname == "" {
		cname = id
	}
	cLower := strings.ToLower(cname)

	// Determine canonical Stack / Project Name
	targetProjectName := strings.TrimSpace(detail.ProjectName)
	targetProjectPath := strings.TrimSpace(detail.ProjectPath)

	isDocktunCore := strings.HasPrefix(cLower, "docktun") ||
		cLower == "cloudflared" ||
		cLower == "wireguard" ||
		cLower == "tailscale" ||
		cLower == "zeroconf" ||
		strings.EqualFold(detail.ProjectName, "docktun") ||
		strings.EqualFold(detail.ProjectName, "tun")

	if isDocktunCore {
		targetProjectName = "docktun"
		if s.cfg.SelfPath != "" {
			targetProjectPath = filepath.Dir(s.cfg.SelfPath)
		} else {
			targetProjectPath = "./"
		}
	}

	if targetProjectName == "" {
		// Check if container name is part of an existing stack (e.g. mediastorm-postgres -> mediastorm)
		if s.db != nil {
			rows, _ := s.db.Query(`SELECT id, name, path FROM projects`)
			if rows != nil {
				var existingID int
				for rows.Next() {
					var pid int
					var pName, pPath string
					if err := rows.Scan(&pid, &pName, &pPath); err == nil {
						pLower := strings.ToLower(pName)
						if strings.HasPrefix(cLower, pLower+"-") || strings.HasPrefix(cLower, pLower+"_") {
							existingID = pid
							break
						}
					}
				}
				rows.Close()
				if existingID > 0 {
					return existingID, nil
				}
			}
		}
		targetProjectName = cname
	}

	// If this project is already registered in SQLite, return existing ID
	if s.db != nil {
		var existingID int
		_ = s.db.QueryRow(`SELECT id FROM projects WHERE LOWER(name) = LOWER(?)`, targetProjectName).Scan(&existingID)
		if existingID > 0 {
			return existingID, nil
		}
	}

	destDir := targetProjectPath
	if destDir == "" {
		destDir = filepath.Join("/opt/projects", targetProjectName)
		if s.cfg.SelfPath != "" {
			destDir = filepath.Join(filepath.Dir(s.cfg.SelfPath), targetProjectName)
		}
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create project directory %s: %w", destDir, err)
	}

	composePath := filepath.Join(destDir, "docker-compose.yml")
	info, statErr := os.Stat(composePath)
	if os.IsNotExist(statErr) || (statErr == nil && info.Size() == 0) {
		img := detail.Image
		if img == "" {
			img = "alpine"
		}
		var b strings.Builder
		b.WriteString("services:\n")
		b.WriteString(fmt.Sprintf("  %s:\n", cname))
		b.WriteString(fmt.Sprintf("    image: %s\n", img))
		b.WriteString(fmt.Sprintf("    container_name: %s\n", cname))
		restartPol := detail.RestartPolicy
		if restartPol == "" {
			restartPol = "unless-stopped"
		}
		b.WriteString(fmt.Sprintf("    restart: %s\n", restartPol))
		if detail.User != "" {
			b.WriteString(fmt.Sprintf("    user: %q\n", detail.User))
		}
		if detail.WorkingDir != "" {
			b.WriteString(fmt.Sprintf("    working_dir: %q\n", detail.WorkingDir))
		}
		if detail.Privileged {
			b.WriteString("    privileged: true\n")
		}
		if len(detail.CapAdd) > 0 {
			b.WriteString("    cap_add:\n")
			for _, cap := range detail.CapAdd {
				b.WriteString(fmt.Sprintf("      - %s\n", cap))
			}
		}
		if detail.Command != "" {
			b.WriteString(fmt.Sprintf("    command: %s\n", detail.Command))
		}
		if len(detail.Mounts) > 0 {
			b.WriteString("    volumes:\n")
			for _, m := range detail.Mounts {
				src := m.Source
				if m.Type == "volume" && m.Name != "" {
					src = m.Name
				}
				if src != "" && m.Destination != "" {
					mode := "rw"
					if !m.RW {
						mode = "ro"
					}
					b.WriteString(fmt.Sprintf("      - %s:%s:%s\n", src, m.Destination, mode))
				}
			}
		}
		envToUse := detail.RawEnv
		if len(envToUse) == 0 {
			envToUse = detail.Env
		}
		if len(envToUse) > 0 {
			b.WriteString("    environment:\n")
			for _, e := range envToUse {
				if !strings.Contains(e, "********") && !strings.HasPrefix(e, "PATH=") {
					b.WriteString(fmt.Sprintf("      - %s\n", e))
				}
			}
		}
		if detail.Ports != "" {
			var portLines []string
			seen := make(map[string]bool)
			for _, part := range strings.Split(detail.Ports, ",") {
				if parsed, ok := parsePortMapping(part); ok {
					if parsed.HostPort != "" && parsed.ContainerPort != "" {
						mapping := parsed.FormatForCompose()
						if !seen[mapping] {
							seen[mapping] = true
							portLines = append(portLines, fmt.Sprintf("      - \"%s\"\n", mapping))
						}
					}
				}
			}
			if len(portLines) > 0 {
				b.WriteString("    ports:\n")
				for _, pl := range portLines {
					b.WriteString(pl)
				}
			}
		}

		var svcNetworks []string
		var externalNetworks []string
		for _, net := range detail.Networks {
			if net != "" && net != "bridge" && net != "host" && net != "none" {
				svcNetworks = append(svcNetworks, net)
				externalNetworks = append(externalNetworks, net)
			}
		}
		defNet := s.DefaultNetwork()
		if detail.ExposureMode == "public" || detail.ExposureMode == "private" {
			foundNet := false
			for _, n := range svcNetworks {
				if n == defNet || n == "docktun" || n == "cloudflared" {
					foundNet = true
					break
				}
			}
			if !foundNet {
				svcNetworks = append(svcNetworks, defNet)
				externalNetworks = append(externalNetworks, defNet)
			}
		}

		if len(svcNetworks) > 0 {
			b.WriteString("    networks:\n")
			for _, sn := range svcNetworks {
				b.WriteString(fmt.Sprintf("      - %s\n", sn))
			}
			b.WriteString("\nnetworks:\n")
			for _, en := range externalNetworks {
				b.WriteString(fmt.Sprintf("  %s:\n    external: true\n", en))
			}
		}

		if err := os.WriteFile(composePath, []byte(b.String()), 0644); err != nil {
			return 0, fmt.Errorf("failed to write compose file: %w", err)
		}
	} else {
		sanitizeComposeFile(composePath)
	}

	envPath := filepath.Join(destDir, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		_ = os.WriteFile(envPath, []byte("# Environment variables for "+cname+"\n"), 0644)
	}

	if s.db != nil {
		_, _ = s.db.Exec(`INSERT OR IGNORE INTO projects (name, path) VALUES (?, ?)`, targetProjectName, destDir)
		_, _ = s.db.Exec(`UPDATE projects SET path = ? WHERE name = ?`, destDir, targetProjectName)
		var projID int
		_ = s.db.QueryRow(`SELECT id FROM projects WHERE LOWER(name) = LOWER(?) OR path = ?`, targetProjectName, destDir).Scan(&projID)
		if projID > 0 {
			return projID, nil
		}
	}

	return 0, nil
}

func (s *DockerContainers) Start(id string) error { return dockerCommand("start", id) }
func (s *DockerContainers) Stop(id string) error { return dockerCommand("stop", id) }
func (s *DockerContainers) Restart(id string) error { return dockerCommand("restart", id) }

type BatchActionResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (s *DockerContainers) BatchAction(ids []string, action string) []BatchActionResult {
	results := make([]BatchActionResult, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(idx int, cID string) {
			defer wg.Done()
			var err error
			switch action {
			case "start":
				err = s.Start(cID)
			case "stop":
				err = s.Stop(cID)
			case "restart":
				err = s.Restart(cID)
			default:
				err = fmt.Errorf("unsupported batch action: %s", action)
			}
			if err != nil {
				results[idx] = BatchActionResult{ID: cID, Success: false, Error: err.Error()}
			} else {
				results[idx] = BatchActionResult{ID: cID, Success: true}
			}
		}(i, id)
	}
	wg.Wait()
	return results
}

func (s *DockerContainers) Remove(id string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, id)
	return dockerCommand(args...)
}

func (s *DockerContainers) Logs(id string, tail int, timestamps bool) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	args := []string{"logs", "--tail", strconv.Itoa(tail)}
	if timestamps {
		args = append(args, "--timestamps")
	}
	args = append(args, id)

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker logs failed: %w", err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func (s *DockerContainers) GetExposure(id string) (ExposureSettings, error) {
	name, err := s.containerName(id)
	if err != nil {
		return ExposureSettings{Mode: "internal"}, err
	}

	ingressMap := s.loadCloudflareIngressMap()
	if ing, ok := ingressMap[name]; ok {
		return ExposureSettings{
			Mode:     "public",
			Hostname: ing.Hostname,
			Target:   ing.Service,
		}, nil
	}

	return s.getExposureFromDB(name), nil
}

func cleanHostname(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimRight(h, "/")
	if idx := strings.Index(h, ":"); idx != -1 {
		h = h[:idx]
	}
	return strings.ToLower(h)
}

func (s *DockerContainers) detectPrimaryPort(nameOrID string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .NetworkSettings.Ports}}", nameOrID)
	out, err := cmd.Output()
	if err == nil {
		var ports map[string]interface{}
		if err := json.Unmarshal(out, &ports); err == nil {
			commonPorts := []string{"80/tcp", "8080/tcp", "3000/tcp", "5000/tcp", "8000/tcp", "8888/tcp", "9000/tcp"}
			for _, cp := range commonPorts {
				if _, ok := ports[cp]; ok {
					return strings.TrimSuffix(cp, "/tcp")
				}
			}
			for p := range ports {
				if strings.HasSuffix(p, "/tcp") {
					return strings.TrimSuffix(p, "/tcp")
				}
			}
		}
	}
	return "80"
}

func (s *DockerContainers) normalizeTarget(containerName, rawTarget string) string {
	target := strings.TrimSpace(rawTarget)
	defaultPort := s.detectPrimaryPort(containerName)
	if target == "" {
		return fmt.Sprintf("http://%s:%s", containerName, defaultPort)
	}

	scheme := "http://"
	rest := target
	if strings.HasPrefix(target, "http://") {
		rest = strings.TrimPrefix(target, "http://")
	} else if strings.HasPrefix(target, "https://") {
		scheme = "https://"
		rest = strings.TrimPrefix(target, "https://")
	} else if strings.HasPrefix(target, "tcp://") {
		scheme = "tcp://"
		rest = strings.TrimPrefix(target, "tcp://")
	}

	hostPart := rest
	portPart := ""
	if idx := strings.Index(rest, "/"); idx != -1 {
		hostPart = rest[:idx]
	}
	if strings.Contains(hostPart, ":") {
		parts := strings.Split(hostPart, ":")
		hostPart = parts[0]
		portPart = parts[1]
	}

	if strings.HasSuffix(hostPart, ".local") {
		hostPart = strings.TrimSuffix(hostPart, ".local")
	}

	if hostPart == "localhost" || hostPart == "127.0.0.1" {
		hostPart = "host.docker.internal"
	}

	if portPart == "" {
		portPart = defaultPort
	}

	return fmt.Sprintf("%s%s:%s", scheme, hostPart, portPart)
}

func (s *DockerContainers) UpdateExposure(id string, settings ExposureSettings) error {
	name, err := s.containerName(id)
	if err != nil {
		return err
	}

	if settings.Mode == "" {
		settings.Mode = "internal"
	}

	// 1. If public or private mode, connect container to default network; otherwise disconnect
	defaultNet := s.DefaultNetwork()
	if settings.Mode == "public" || settings.Mode == "private" {
		if err := s.ConnectNetwork(name, defaultNet); err != nil {
			log.Printf("[docktun] connect network %s for %s: %v", defaultNet, name, err)
		}
		if exec.Command("docker", "network", "inspect", "cloudflared").Run() == nil && defaultNet != "cloudflared" {
			_ = s.ConnectNetwork(name, "cloudflared")
		}
	} else {
		_ = s.DisconnectNetwork(name, defaultNet)
		_ = s.DisconnectNetwork(name, "cloudflared")
	}

	prevExp := s.getExposureFromDB(name)

	// 2. Update Cloudflare ingress ONLY if currently public or was previously public
	if s.configMgr != nil && (settings.Mode == "public" || prevExp.Mode == "public") {
		if err := s.syncCloudflareIngress(name, settings); err != nil {
			return fmt.Errorf("sync ingress config: %w", err)
		}
	}

	// 3. Persist in database
	cleanSettings := settings
	cleanSettings.Hostname = cleanHostname(settings.Hostname)
	if cleanSettings.Mode == "public" {
		cleanSettings.Target = s.normalizeTarget(name, settings.Target)
	}
	if err := s.saveExposureToDB(name, cleanSettings); err != nil {
		return err
	}

	// 4. Synchronize Zero Trust Access Application & Policy if enabled
	if cleanSettings.Mode == "public" && cleanSettings.ZeroTrustEnabled && cleanSettings.Hostname != "" && s.cfAPI != nil {
		go func(cName string, set ExposureSettings) {
			appID, err := s.cfAPI.SyncZeroTrustAccess(ZeroTrustPolicyOptions{
				Hostname:        set.Hostname,
				AuthType:        set.ZeroTrustAuthType,
				Emails:          set.ZeroTrustEmails,
				SessionDuration: set.ZeroTrustSession,
			})
			if err != nil {
				log.Printf("[docktun] Cloudflare Zero Trust sync error for %s: %v", set.Hostname, err)
			} else if appID != "" {
				_ = s.updateZeroTrustAppID(cName, appID)
				log.Printf("[docktun] Cloudflare Zero Trust Access active for %s (app_id: %s)", set.Hostname, appID)
			}
		}(name, cleanSettings)
	} else if (!cleanSettings.ZeroTrustEnabled || cleanSettings.Mode != "public") && s.cfAPI != nil && prevExp.ZeroTrustAppID != "" {
		go func(appID string) {
			if err := s.cfAPI.DeleteZeroTrustAccess(appID); err != nil {
				log.Printf("[docktun] Cloudflare Zero Trust delete notice: %v", err)
			}
		}(prevExp.ZeroTrustAppID)
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = s.ReconcileNetworks()
	}()

	return nil
}

func (s *DockerContainers) syncCloudflareIngress(containerName string, settings ExposureSettings) error {
	if s.configMgr == nil {
		return nil
	}

	raw, err := s.configMgr.Read()
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
			if settings.Mode == "public" {
				return fmt.Errorf("Cloudflare tunnel is not configured (/etc/cloudflared/config.yml not found). Configure Cloudflare in Remote Access first, or choose Private (Tailscale / WireGuard) mode")
			}
			return nil
		}
		return err
	}

	var doc cfConfigDoc
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return fmt.Errorf("unmarshal cloudflared config: %w", err)
	}

	cleanHost := cleanHostname(settings.Hostname)

	// Filter out any existing rule for this container or hostname
	newIngress := make([]cfIngressRule, 0, len(doc.Ingress)+1)
	for _, rule := range doc.Ingress {
		// Ignore catch-all rule during filter
		if rule.Service == "http_status:404" {
			continue
		}
		// If rule matches container name or hostname
		if strings.Contains(rule.Service, containerName) || (cleanHost != "" && cleanHostname(rule.Hostname) == cleanHost) {
			continue
		}
		newIngress = append(newIngress, rule)
	}

	// If public mode, add new rule
	if settings.Mode == "public" && cleanHost != "" {
		target := s.normalizeTarget(containerName, settings.Target)
		rule := cfIngressRule{
			Hostname: cleanHost,
			Service:  target,
		}
		if strings.HasPrefix(target, "https://") {
			rule.OriginRequest = &cfOriginRequest{
				NoTLSVerify: true,
			}
		}
		newIngress = append(newIngress, rule)
	}

	// Always ensure catch-all 404 is at the end
	newIngress = append(newIngress, cfIngressRule{
		Service: "http_status:404",
	})

	doc.Ingress = newIngress

	updatedYaml, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal updated cloudflared config: %w", err)
	}

	if err := s.configMgr.Write(string(updatedYaml), 1); err != nil {
		return err
	}

	// Reload cloudflared synchronously so changes take effect immediately
	if s.cloudflared != nil {
		if err := s.cloudflared.Restart(); err != nil {
			log.Printf("[docktun] cloudflared restart error: %v", err)
		}
	}

	// Automatically create or verify DNS CNAME record in Cloudflare
	if settings.Mode == "public" && cleanHost != "" && s.cfAPI != nil && doc.Tunnel != "" {
		go func(host, tid string) {
			if err := s.cfAPI.SyncDNSRecord(host, tid); err != nil {
				log.Printf("[docktun] Cloudflare DNS sync notice for %s: %v", host, err)
			}
		}(cleanHost, doc.Tunnel)
	}

	return nil
}

func (s *DockerContainers) loadCloudflareIngressMap() map[string]cfIngressRule {
	result := make(map[string]cfIngressRule)
	if s.configMgr == nil {
		return result
	}

	raw, err := s.configMgr.Read()
	if err != nil {
		return result
	}

	var doc cfConfigDoc
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return result
	}

	for _, rule := range doc.Ingress {
		if rule.Hostname == "" || rule.Service == "http_status:404" {
			continue
		}
		// Extract container name from service e.g. http://my-app:8080 -> my-app
		svc := strings.TrimPrefix(rule.Service, "http://")
		svc = strings.TrimPrefix(svc, "https://")
		hostPart := strings.Split(svc, ":")[0]
		hostPart = strings.Split(hostPart, "/")[0]
		if hostPart != "" {
			result[hostPart] = rule
		}
	}

	return result
}

func (s *DockerContainers) loadAllExposuresFromDB() map[string]ExposureSettings {
	res := make(map[string]ExposureSettings)
	if s.db == nil {
		return res
	}

	rows, err := s.db.Query(`SELECT container_name, mode, hostname, target, private_type, private_host, private_port, notes, COALESCE(zero_trust_enabled, 0), COALESCE(zero_trust_emails, ''), COALESCE(zero_trust_app_id, ''), COALESCE(zero_trust_auth_type, 'email'), COALESCE(zero_trust_session, '24h') FROM container_exposures`)
	if err != nil {
		return res
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var exp ExposureSettings
		var pPort sql.NullInt64
		var ztInt int
		if err := rows.Scan(&name, &exp.Mode, &exp.Hostname, &exp.Target, &exp.PrivateType, &exp.PrivateHost, &pPort, &exp.Notes, &ztInt, &exp.ZeroTrustEmails, &exp.ZeroTrustAppID, &exp.ZeroTrustAuthType, &exp.ZeroTrustSession); err == nil {
			if pPort.Valid {
				exp.PrivatePort = int(pPort.Int64)
			}
			exp.ZeroTrustEnabled = ztInt == 1
			res[name] = exp
		}
	}
	return res
}

func (s *DockerContainers) getExposureFromDB(name string) ExposureSettings {
	if s.db == nil {
		return ExposureSettings{Mode: "internal"}
	}

	var exp ExposureSettings
	var pPort sql.NullInt64
	var ztInt int
	err := s.db.QueryRow(
		`SELECT mode, hostname, target, private_type, private_host, private_port, notes, COALESCE(zero_trust_enabled, 0), COALESCE(zero_trust_emails, ''), COALESCE(zero_trust_app_id, ''), COALESCE(zero_trust_auth_type, 'email'), COALESCE(zero_trust_session, '24h') FROM container_exposures WHERE container_name = ?`,
		name,
	).Scan(&exp.Mode, &exp.Hostname, &exp.Target, &exp.PrivateType, &exp.PrivateHost, &pPort, &exp.Notes, &ztInt, &exp.ZeroTrustEmails, &exp.ZeroTrustAppID, &exp.ZeroTrustAuthType, &exp.ZeroTrustSession)
	if err != nil {
		return ExposureSettings{Mode: "internal"}
	}
	if pPort.Valid {
		exp.PrivatePort = int(pPort.Int64)
	}
	exp.ZeroTrustEnabled = ztInt == 1
	return exp
}

func (s *DockerContainers) saveExposureToDB(name string, exp ExposureSettings) error {
	if s.db == nil {
		return nil
	}

	ztInt := 0
	if exp.ZeroTrustEnabled {
		ztInt = 1
	}

	if exp.ZeroTrustAuthType == "" {
		exp.ZeroTrustAuthType = "email"
	}
	if exp.ZeroTrustSession == "" {
		exp.ZeroTrustSession = "24h"
	}

	_, err := s.db.Exec(`
		INSERT INTO container_exposures (container_name, mode, hostname, target, private_type, private_host, private_port, notes, zero_trust_enabled, zero_trust_emails, zero_trust_app_id, zero_trust_auth_type, zero_trust_session, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(container_name) DO UPDATE SET
			mode = excluded.mode,
			hostname = excluded.hostname,
			target = excluded.target,
			private_type = excluded.private_type,
			private_host = excluded.private_host,
			private_port = excluded.private_port,
			notes = excluded.notes,
			zero_trust_enabled = excluded.zero_trust_enabled,
			zero_trust_emails = excluded.zero_trust_emails,
			zero_trust_app_id = CASE WHEN excluded.zero_trust_app_id != '' THEN excluded.zero_trust_app_id ELSE container_exposures.zero_trust_app_id END,
			zero_trust_auth_type = excluded.zero_trust_auth_type,
			zero_trust_session = excluded.zero_trust_session,
			updated_at = CURRENT_TIMESTAMP
	`, name, exp.Mode, exp.Hostname, exp.Target, exp.PrivateType, exp.PrivateHost, exp.PrivatePort, exp.Notes, ztInt, exp.ZeroTrustEmails, exp.ZeroTrustAppID, exp.ZeroTrustAuthType, exp.ZeroTrustSession)
	return err
}

func (s *DockerContainers) updateZeroTrustAppID(name, appID string) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`UPDATE container_exposures SET zero_trust_app_id = ? WHERE container_name = ?`, appID, name)
	return err
}

func (s *DockerContainers) containerName(id string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Name}}", id)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect name failed: %w", err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "/"), nil
}

func uptimeFromStartedAt(value string) string {
	if value == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	diff := time.Since(t)
	if diff < time.Minute {
		return fmt.Sprintf("%ds", int(diff.Seconds()))
	}
	if diff < time.Hour {
		return fmt.Sprintf("%dm", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		mins := int(diff.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	days := int(diff.Hours()) / 24
	hours := int(diff.Hours()) % 24
	return fmt.Sprintf("%dd %dh", days, hours)
}

func dockerCommand(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *DockerContainers) Exec(id string, command string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "exec", id, "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return string(out), exitCode, nil
}

func (s *DockerContainers) SystemDF() (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "system", "df", "--format", "{{json .}}")
	out, err := cmd.Output()
	result := make(map[string]interface{})
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		var items []interface{}
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(line), &m); err == nil {
				items = append(items, m)
			}
		}
		result["items"] = items
	} else {
		cmdPlain := exec.CommandContext(ctx, "docker", "system", "df")
		plainOut, _ := cmdPlain.CombinedOutput()
		result["raw"] = string(plainOut)
	}
	return result, nil
}

func (s *DockerContainers) SystemPrune() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var b strings.Builder
	// 1. Prune dangling and unused images safely (preserves all containers and volumes)
	imgOut, err := exec.CommandContext(ctx, "docker", "image", "prune", "-f").CombinedOutput()
	if err == nil && len(imgOut) > 0 {
		b.WriteString("Images:\n" + strings.TrimSpace(string(imgOut)) + "\n\n")
	}
	// 2. Prune build cache safely
	bcOut, err := exec.CommandContext(ctx, "docker", "builder", "prune", "-f").CombinedOutput()
	if err == nil && len(bcOut) > 0 {
		b.WriteString("Build Cache:\n" + strings.TrimSpace(string(bcOut)) + "\n")
	}
	if b.Len() == 0 {
		return "Total reclaimed space: 0B (all containers and volumes safely preserved)", nil
	}
	return b.String(), nil
}

func (s *DockerContainers) ProbeTarget(targetURL string) (map[string]interface{}, error) {
	result := map[string]interface{}{
		"reachable":   false,
		"status_code": 0,
		"latency_ms":  0,
		"error":       "",
	}
	if targetURL == "" {
		result["error"] = "Target URL is empty"
		return result, nil
	}

	raw := targetURL
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		result["error"] = fmt.Sprintf("Invalid target URL: %v", err)
		return result, nil
	}

	start := time.Now()
	client := &http.Client{
		Timeout: 4 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(parsed.String())
	latency := time.Since(start).Milliseconds()
	result["latency_ms"] = latency

	if err != nil {
		result["error"] = err.Error()
		return result, nil
	}
	defer resp.Body.Close()

	result["reachable"] = true
	result["status_code"] = resp.StatusCode
	return result, nil
}

type UpdateCheckResult struct {
	ContainerID     string `json:"container_id"`
	ContainerName   string `json:"container_name"`
	Image           string `json:"image"`
	CurrentDigest   string `json:"current_digest"`
	LatestDigest    string `json:"latest_digest"`
	UpdateAvailable bool   `json:"update_available"`
	CheckedAt       string `json:"checked_at"`
	Error           string `json:"error,omitempty"`
}

func (s *DockerContainers) CheckContainerUpdate(id string) (UpdateCheckResult, error) {
	detail, err := s.Details(id)
	if err != nil {
		return UpdateCheckResult{}, err
	}

	cname := strings.TrimPrefix(detail.Name, "/")
	res := UpdateCheckResult{
		ContainerID:   detail.ID,
		ContainerName: cname,
		Image:         detail.Image,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	if detail.Image == "" {
		res.Error = "no image specified"
		s.saveUpdateToDB(res)
		return res, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 0. Detect DockTun container or locally built image
	if isDocktunContainer(cname, detail.Image) {
		res.CurrentDigest = "local-git"
		commitSHA, commitMsg, ghErr := fetchLatestGitHubCommit(ctx, "oOflyeyesOo", "docktun", "main")
		if ghErr == nil && commitSHA != "" {
			res.LatestDigest = commitSHA
			res.Error = fmt.Sprintf("DockTun is built locally. Latest upstream commit: %s (%s). Update via host: git pull && docker compose up -d --build docktun", commitSHA, commitMsg)
		} else {
			res.LatestDigest = "git-main"
			res.Error = "DockTun is built locally. Update via host: git pull && docker compose up -d --build docktun"
		}
		res.UpdateAvailable = false
		s.saveUpdateToDB(res)
		return res, nil
	}

	// 1. Inspect local image RepoDigests and ID
	inspectCmd := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{json .RepoDigests}}\t{{.Id}}", detail.Image)
	var localDigests []string
	if out, err := inspectCmd.Output(); err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), "\t")
		if len(parts) >= 1 {
			var repoDigests []string
			if json.Unmarshal([]byte(parts[0]), &repoDigests) == nil {
				for _, rd := range repoDigests {
					if idx := strings.Index(rd, "@"); idx != -1 {
						localDigests = append(localDigests, rd[idx+1:])
					} else {
						localDigests = append(localDigests, rd)
					}
				}
			}
		}
		if len(parts) >= 2 {
			localId := strings.TrimSpace(parts[1])
			if localId != "" {
				localDigests = append(localDigests, localId)
			}
		}
	}

	if len(localDigests) > 0 {
		res.CurrentDigest = localDigests[0]
	}

	// 2. Fetch remote manifest digest
	reg, repo, tag := parseImageRef(detail.Image)
	latestDigest, subDigests, err := fetchRemoteManifestDigest(ctx, reg, repo, tag)
	if err != nil {
		// Fallback: use docker manifest inspect CLI
		manifestCmd := exec.CommandContext(ctx, "docker", "manifest", "inspect", detail.Image)
		if mOut, mErr := manifestCmd.Output(); mErr == nil {
			var mDoc struct {
				Manifests []struct {
					Digest string `json:"digest"`
				} `json:"manifests"`
				Config struct {
					Digest string `json:"digest"`
				} `json:"config"`
			}
			if json.Unmarshal(mOut, &mDoc) == nil {
				for _, m := range mDoc.Manifests {
					if m.Digest != "" {
						subDigests = append(subDigests, m.Digest)
					}
				}
				if len(subDigests) > 0 {
					latestDigest = subDigests[0]
					err = nil
				} else if mDoc.Config.Digest != "" {
					latestDigest = mDoc.Config.Digest
					err = nil
				}
			}
		}
	}

	if err != nil {
		if strings.Contains(err.Error(), "does not host this image") || strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "404") {
			res.Error = "Local build or private image (updates managed locally)"
		} else {
			res.Error = fmt.Sprintf("unable to check registry: %v", err)
		}
		s.saveUpdateToDB(res)
		return res, nil
	}

	res.LatestDigest = latestDigest

	// 3. Compare local digests with remote digests
	allRemote := append([]string{latestDigest}, subDigests...)
	matched := false
	for _, rem := range allRemote {
		if rem == "" {
			continue
		}
		for _, loc := range localDigests {
			if strings.EqualFold(loc, rem) || strings.HasSuffix(loc, rem) || strings.HasSuffix(rem, loc) {
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}

	res.UpdateAvailable = !matched && (latestDigest != "")
	s.saveUpdateToDB(res)
	return res, nil
}

func (s *DockerContainers) CheckAllUpdates() ([]UpdateCheckResult, error) {
	containers, err := s.List()
	if err != nil {
		return nil, err
	}

	results := make([]UpdateCheckResult, len(containers))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for i, c := range containers {
		wg.Add(1)
		go func(idx int, cont Container) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res, err := s.CheckContainerUpdate(cont.ID)
			if err != nil {
				results[idx] = UpdateCheckResult{
					ContainerID:   cont.ID,
					ContainerName: cont.Name,
					Image:         cont.Image,
					Error:         err.Error(),
					CheckedAt:     time.Now().UTC().Format(time.RFC3339),
				}
			} else {
				results[idx] = res
			}
		}(i, c)
	}

	wg.Wait()
	return results, nil
}

func (s *DockerContainers) UpdateContainer(id string, userID int, ip string) (string, error) {
	detail, err := s.Details(id)
	if err != nil {
		return "", fmt.Errorf("failed to get container details: %w", err)
	}

	cname := strings.TrimPrefix(detail.Name, "/")
	image := detail.Image
	if image == "" {
		return "", fmt.Errorf("container has no image specified")
	}

	var logOutput strings.Builder
	appendLog := func(msg string) {
		log.Printf("[docktun-update] %s", msg)
		logOutput.WriteString(msg + "\n")
	}

	appendLog(fmt.Sprintf("[STEP 1/4] Pulling latest image: %s...", image))
	pullCtx, pullCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer pullCancel()

	pullCmd := exec.CommandContext(pullCtx, "docker", "pull", image)
	pullOut, err := pullCmd.CombinedOutput()
	appendLog(strings.TrimSpace(string(pullOut)))
	if err != nil {
		return logOutput.String(), fmt.Errorf("failed to pull image %s: %s (%w)", image, strings.TrimSpace(string(pullOut)), err)
	}

	// Step 2: Determine recreation strategy
	compProj := ""
	compDir := ""
	compFile := ""
	compService := ""

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspectCmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .Config.Labels}}", id)
	if out, err := inspectCmd.Output(); err == nil {
		var labels map[string]string
		if err := json.Unmarshal(out, &labels); err == nil {
			compProj = labels["com.docker.compose.project"]
			compDir = labels["com.docker.compose.project.working_dir"]
			compFile = labels["com.docker.compose.project.config_files"]
			compService = labels["com.docker.compose.service"]
		}
	}

	if compService == "" {
		compService = cname
	}

	// Path translation: Check if compDir exists or can be mapped to container's /opt/projects
	validComposeDir := ""
	if compDir != "" {
		if fi, err := os.Stat(compDir); err == nil && fi.IsDir() {
			validComposeDir = compDir
		} else {
			if idx := strings.Index(compDir, "/projects/"); idx != -1 {
				mapped := filepath.Join("/opt/projects", compDir[idx+len("/projects/"):])
				if fi, err := os.Stat(mapped); err == nil && fi.IsDir() {
					validComposeDir = mapped
				}
			}
			if validComposeDir == "" {
				if idx := strings.Index(compDir, "/compose/"); idx != -1 {
					mapped := filepath.Join("/opt/projects", compDir[idx+len("/compose/"):])
					if fi, err := os.Stat(mapped); err == nil && fi.IsDir() {
						validComposeDir = mapped
					}
				}
			}
			if validComposeDir == "" {
				cDir := filepath.Join("/opt/projects", cname)
				if fi, err := os.Stat(cDir); err == nil && fi.IsDir() {
					validComposeDir = cDir
				}
			}
		}
	}

	if validComposeDir == "" && detail.ProjectPath != "" {
		if fi, err := os.Stat(detail.ProjectPath); err == nil && fi.IsDir() {
			validComposeDir = detail.ProjectPath
		} else if idx := strings.Index(detail.ProjectPath, "/projects/"); idx != -1 {
			mapped := filepath.Join("/opt/projects", detail.ProjectPath[idx+len("/projects/"):])
			if fi, err := os.Stat(mapped); err == nil && fi.IsDir() {
				validComposeDir = mapped
			}
		}
	}

	recreated := false

	// Strategy A: Existing valid compose stack
	if validComposeDir != "" {
		composeCandidate := compFile
		if composeCandidate == "" || !fileExists(composeCandidate) {
			for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
				p := filepath.Join(validComposeDir, name)
				if fileExists(p) {
					composeCandidate = p
					break
				}
			}
		}
		if composeCandidate != "" && fileExists(composeCandidate) {
			sanitizeComposeFile(composeCandidate)
		}

		appendLog(fmt.Sprintf("[STEP 2/4] Updating via Docker Compose in %s (service: %s)...", validComposeDir, compService))
		var composeArgs []string
		if composeCandidate != "" && fileExists(composeCandidate) {
			composeArgs = append(composeArgs, "-f", composeCandidate)
		} else {
			composeArgs = append(composeArgs, "--project-directory", validComposeDir)
		}
		composeArgs = append(composeArgs, "up", "-d", "--no-deps", "--force-recreate", compService)

		upCmd := exec.Command("docker", append([]string{"compose"}, composeArgs...)...)
		upCmd.Dir = validComposeDir
		upOut, upErr := upCmd.CombinedOutput()
		if upErr == nil {
			appendLog(strings.TrimSpace(string(upOut)))
			recreated = true
		} else {
			outStr := string(upOut)
			if strings.Contains(outStr, "Conflict") && strings.Contains(outStr, "already in use") {
				appendLog(fmt.Sprintf("[AUTO-RESOLVE] Staging existing container %s to adopt into Compose without conflict...", cname))
				backupName := fmt.Sprintf("%s_bk_%d", cname, time.Now().Unix())
				_ = exec.Command("docker", "stop", detail.ID).Run()
				if renameErr := exec.Command("docker", "rename", detail.ID, backupName).Run(); renameErr == nil {
					retryCmd := exec.Command("docker", append([]string{"compose"}, composeArgs...)...)
					retryCmd.Dir = validComposeDir
					retryOut, retryErr := retryCmd.CombinedOutput()
					if retryErr == nil {
						recreated = true
						_ = exec.Command("docker", "rm", "-f", backupName).Run()
						appendLog(strings.TrimSpace(string(retryOut)))
						appendLog(fmt.Sprintf("✔ [SUCCESS] Container %s adopted and recreated cleanly by Docker Compose!", cname))
					} else {
						_ = exec.Command("docker", "rm", "-f", cname).Run()
						_ = exec.Command("docker", "rename", backupName, cname).Run()
						_ = exec.Command("docker", "start", cname).Run()
						appendLog(fmt.Sprintf("[NOTE] Compose retry notice: %s. Attempting direct recreation...", strings.TrimSpace(string(retryOut))))
					}
				}
			} else {
				appendLog(fmt.Sprintf("[NOTE] Compose notice: %s. Attempting direct recreation...", strings.TrimSpace(string(upOut))))
			}
		}
	}

	// Strategy B: Scaffold compose and recreate
	// If the container has an external compose project (compProj != "") but its compose directory
	// is not under DockTun's /opt/projects, DO NOT scaffold a disjoint 1-service project, because that would
	// sever the container from its stack and network. Instead, proceed directly to native recreate.
	if !recreated && compProj != "" && validComposeDir == "" {
		appendLog(fmt.Sprintf("[NOTE] Container belongs to external Compose stack '%s'. Using native recreation with preserved stack networks & aliases...", compProj))
	} else if !recreated {
		appendLog(fmt.Sprintf("[STEP 2/4] Ensuring compose configuration for %s...", cname))
		_, _ = s.EnsureProjectForContainer(id)

		destDir := filepath.Join("/opt/projects", cname)
		if s.cfg.SelfPath != "" {
			destDir = filepath.Join(filepath.Dir(s.cfg.SelfPath), cname)
		}
		_ = os.MkdirAll(destDir, 0755)
		composeFilePath := filepath.Join(destDir, "docker-compose.yml")

		if fileExists(composeFilePath) {
			sanitizeComposeFile(composeFilePath)
			appendLog(fmt.Sprintf("[STEP 3/4] Recreating container %s via Docker Compose in %s...", cname, destDir))
			if s.isSelfContainer(detail.ID, cname) {
				upCmd := exec.Command("docker", "compose", "-f", composeFilePath, "up", "-d", "--force-recreate")
				upCmd.Dir = destDir
				upOut, upErr := upCmd.CombinedOutput()
				if upErr == nil {
					recreated = true
					appendLog(strings.TrimSpace(string(upOut)))
					appendLog(fmt.Sprintf("✔ [SUCCESS] Container %s recreated cleanly by Docker Compose!", cname))
				} else {
					appendLog(fmt.Sprintf("[NOTE] Compose recreation notice: %s. Falling back to native recreation...", strings.TrimSpace(string(upOut))))
				}
			} else {
				backupName := fmt.Sprintf("%s_bk_%d", cname, time.Now().Unix())
				_ = exec.Command("docker", "stop", detail.ID).Run()
				if renameErr := exec.Command("docker", "rename", detail.ID, backupName).Run(); renameErr == nil {
					upCmd := exec.Command("docker", "compose", "-f", composeFilePath, "up", "-d", "--force-recreate")
					upCmd.Dir = destDir
					upOut, upErr := upCmd.CombinedOutput()
					if upErr == nil {
						recreated = true
						_ = exec.Command("docker", "rm", "-f", backupName).Run()
						appendLog(strings.TrimSpace(string(upOut)))
						appendLog(fmt.Sprintf("✔ [SUCCESS] Container %s recreated cleanly by Docker Compose!", cname))
					} else {
						_ = exec.Command("docker", "rm", "-f", cname).Run()
						_ = exec.Command("docker", "rename", backupName, cname).Run()
						_ = exec.Command("docker", "start", cname).Run()
						appendLog(fmt.Sprintf("[NOTE] Compose recreation notice: %s. Falling back to native Docker Engine recreation...", strings.TrimSpace(string(upOut))))
					}
				} else {
					appendLog(fmt.Sprintf("[NOTE] Could not stage container for Compose recreate: %v. Proceeding to native recreation...", renameErr))
				}
			}
		}
	}

	// Strategy C: Direct native Docker Engine recreate fallback with safe rollback
	if !recreated {
		appendLog(fmt.Sprintf("[STEP 3/4] Performing native Docker recreate for container %s...", cname))
		fallbackErr := s.recreateContainerDirect(detail, image, appendLog)
		if fallbackErr != nil {
			return logOutput.String(), fmt.Errorf("failed to recreate container: %w", fallbackErr)
		}
		recreated = true
	}

	// Step 4: Network and Exposure
	if detail.ExposureMode == "public" || detail.ExposureMode == "private" {
		netName := s.DefaultNetwork()
		appendLog(fmt.Sprintf("[NETWORK] Ensuring connection to %s network...", netName))
		_ = s.ConnectNetwork(cname, netName)
	}

	s.setContainerUpToDate(id, cname, image)

	if s.audit != nil {
		s.audit.Log(userID, "container_update", fmt.Sprintf("Updated container %s (%s) to latest image", cname, image), ip)
	}

	appendLog(fmt.Sprintf("✔ [SUCCESS] Container %s updated and running successfully!", cname))
	return logOutput.String(), nil
}

func (s *DockerContainers) recreateContainerDirect(detail ContainerDetail, newImage string, appendLog func(string)) error {
	cname := strings.TrimPrefix(detail.Name, "/")
	appendLog(fmt.Sprintf("Recreating container %s natively with image %s...", cname, newImage))

	var primaryNet string
	var extraNets []string

	// Prioritize internal/stack networks (e.g. media_default) over ingress bridge networks (docktun, cloudflared)
	var stackNets []string
	var ingressNets []string
	for _, net := range detail.Networks {
		if net == "bridge" || net == "host" || net == "none" || net == "" {
			continue
		}
		if net == "docktun" || net == "cloudflared" {
			ingressNets = append(ingressNets, net)
		} else {
			stackNets = append(stackNets, net)
		}
	}

	orderedNets := append(stackNets, ingressNets...)
	for _, net := range orderedNets {
		if primaryNet == "" {
			primaryNet = net
		} else {
			extraNets = append(extraNets, net)
		}
	}

	restartPol := detail.RestartPolicy
	if restartPol == "" || restartPol == "no" {
		restartPol = "unless-stopped"
	}

	var runArgs []string
	runArgs = append(runArgs, "create", "--name", cname, "--restart", restartPol)
	if primaryNet != "" {
		runArgs = append(runArgs, "--network", primaryNet)
		if aliases, ok := detail.NetworkAliases[primaryNet]; ok {
			for _, a := range aliases {
				if a != "" && a != cname && !strings.HasPrefix(detail.ID, a) {
					runArgs = append(runArgs, "--network-alias", a)
				}
			}
		}
	}

	if detail.User != "" {
		runArgs = append(runArgs, "--user", detail.User)
	}
	if detail.WorkingDir != "" {
		runArgs = append(runArgs, "-w", detail.WorkingDir)
	}
	if detail.Privileged {
		runArgs = append(runArgs, "--privileged")
	}
	for _, cap := range detail.CapAdd {
		if cap != "" {
			runArgs = append(runArgs, "--cap-add", cap)
		}
	}
	for k, v := range detail.Labels {
		if k != "" && !strings.HasPrefix(k, "com.docker.compose.version") {
			runArgs = append(runArgs, "--label", fmt.Sprintf("%s=%s", k, v))
		}
	}
	if detail.ProjectName != "" {
		if _, hasProj := detail.Labels["com.docker.compose.project"]; !hasProj {
			runArgs = append(runArgs, "--label", fmt.Sprintf("com.docker.compose.project=%s", detail.ProjectName))
			runArgs = append(runArgs, "--label", fmt.Sprintf("com.docker.compose.service=%s", cname))
		}
	}

	if detail.Ports != "" {
		seen := make(map[string]bool)
		for _, part := range strings.Split(detail.Ports, ",") {
			if parsed, ok := parsePortMapping(part); ok {
				if parsed.HostPort != "" && parsed.ContainerPort != "" {
					runPort := parsed.FormatForDockerRun()
					if !seen[runPort] {
						seen[runPort] = true
						runArgs = append(runArgs, "-p", runPort)
					}
				} else if parsed.ContainerPort != "" && !seen[parsed.ContainerPort] {
					seen[parsed.ContainerPort] = true
					runArgs = append(runArgs, "--expose", parsed.ContainerPort)
				}
			}
		}
	}

	for _, m := range detail.Mounts {
		src := m.Source
		if m.Type == "volume" && m.Name != "" {
			src = m.Name
		}
		if src != "" && m.Destination != "" {
			mode := "rw"
			if !m.RW {
				mode = "ro"
			}
			runArgs = append(runArgs, "-v", fmt.Sprintf("%s:%s:%s", src, m.Destination, mode))
		}
	}

	envToUse := detail.RawEnv
	if len(envToUse) == 0 {
		envToUse = detail.Env
	}
	for _, e := range envToUse {
		if !strings.Contains(e, "********") && !strings.HasPrefix(e, "PATH=") && !strings.HasPrefix(e, "HOSTNAME=") {
			runArgs = append(runArgs, "-e", e)
		}
	}

	runArgs = append(runArgs, newImage)
	if len(detail.Cmd) > 0 {
		runArgs = append(runArgs, detail.Cmd...)
	} else if detail.Command != "" {
		cleanedCmd := strings.Trim(strings.TrimSpace(detail.Command), "\"")
		if cleanedCmd != "" && !strings.HasSuffix(cleanedCmd, "…") && !strings.HasSuffix(cleanedCmd, "...") {
			runArgs = append(runArgs, strings.Fields(cleanedCmd)...)
		}
	}

	if s.isSelfContainer(detail.ID, cname) {
		appendLog(fmt.Sprintf("✔ [SUCCESS] %s self-update initiated! Restarting container with image %s in background...", cname, newImage))
		selfArgs := append([]string{"run", "-d"}, runArgs[1:]...)
		detachScript := fmt.Sprintf("sleep 1 && docker stop %s && docker rm -f %s && docker %s", detail.ID, detail.ID, strings.Join(selfArgs, " "))
		bgCmd := exec.Command("sh", "-c", detachScript)
		_ = bgCmd.Start()
		return nil
	}

	backupName := fmt.Sprintf("%s_bk_%d", cname, time.Now().Unix())
	appendLog(fmt.Sprintf("[SAFEGUARD] Creating rollback backup of %s as %s...", cname, backupName))

	// Stop original and rename to backup
	_ = exec.Command("docker", "stop", detail.ID).Run()
	if renameErr := exec.Command("docker", "rename", detail.ID, backupName).Run(); renameErr != nil {
		appendLog(fmt.Sprintf("[WARN] Failed to rename container to backup: %v. Proceeding carefully...", renameErr))
		_ = exec.Command("docker", "rm", detail.ID).Run()
	}

	// 1. Create container in Created state with primary network
	cmd := exec.Command("docker", runArgs...)
	out, err := cmd.CombinedOutput()
	appendLog(strings.TrimSpace(string(out)))
	if err != nil {
		appendLog(fmt.Sprintf("[ERROR] Recreating container failed: %s (%v)", strings.TrimSpace(string(out)), err))
		appendLog(fmt.Sprintf("[ROLLBACK] Initiating automatic rollback to backup %s...", backupName))

		_ = exec.Command("docker", "rm", "-f", cname).Run()

		if rbRenameErr := exec.Command("docker", "rename", backupName, cname).Run(); rbRenameErr != nil {
			appendLog(fmt.Sprintf("[FATAL] Rollback rename failed: %v. Old container is still available as %s", rbRenameErr, backupName))
			_ = exec.Command("docker", "start", backupName).Run()
			return fmt.Errorf("recreation failed: %s; rollback rename failed: %v", strings.TrimSpace(string(out)), rbRenameErr)
		}

		_ = exec.Command("docker", "start", cname).Run()
		appendLog(fmt.Sprintf("[ROLLBACK] Original container %s safely restored and restarted!", cname))
		return fmt.Errorf("container recreation failed: %s (original container was rolled back and restarted)", strings.TrimSpace(string(out)))
	}

	// 2. Connect all extra networks (e.g. docktun, cloudflared) BEFORE starting the container!
	for _, net := range extraNets {
		netCmd := []string{"network", "connect"}
		if aliases, ok := detail.NetworkAliases[net]; ok {
			for _, a := range aliases {
				if a != "" && a != cname && !strings.HasPrefix(detail.ID, a) {
					netCmd = append(netCmd, "--alias", a)
				}
			}
		}
		netCmd = append(netCmd, net, cname)
		if connectOut, connectErr := exec.Command("docker", netCmd...).CombinedOutput(); connectErr != nil {
			appendLog(fmt.Sprintf("[NOTE] Connecting network %s notice: %s", net, strings.TrimSpace(string(connectOut))))
		} else {
			appendLog(fmt.Sprintf("[NETWORK] Connected network %s prior to container start.", net))
		}
	}

	// 3. Start the container with all networks and aliases fully active
	startCmd := exec.Command("docker", "start", cname)
	startOut, startErr := startCmd.CombinedOutput()
	appendLog(strings.TrimSpace(string(startOut)))
	if startErr != nil {
		appendLog(fmt.Sprintf("[ERROR] Starting container failed: %s (%v)", strings.TrimSpace(string(startOut)), startErr))
		appendLog(fmt.Sprintf("[ROLLBACK] Initiating automatic rollback to backup %s...", backupName))

		_ = exec.Command("docker", "rm", "-f", cname).Run()

		if rbRenameErr := exec.Command("docker", "rename", backupName, cname).Run(); rbRenameErr != nil {
			appendLog(fmt.Sprintf("[FATAL] Rollback rename failed: %v. Old container is still available as %s", rbRenameErr, backupName))
			_ = exec.Command("docker", "start", backupName).Run()
			return fmt.Errorf("start failed: %s; rollback rename failed: %v", strings.TrimSpace(string(startOut)), rbRenameErr)
		}

		_ = exec.Command("docker", "start", cname).Run()
		appendLog(fmt.Sprintf("[ROLLBACK] Original container %s safely restored and restarted!", cname))
		return fmt.Errorf("container start failed: %s (original container was rolled back and restarted)", strings.TrimSpace(string(startOut)))
	}

	_ = exec.Command("docker", "rm", "-f", backupName).Run()
	appendLog(fmt.Sprintf("[SAFEGUARD] Backup %s pruned after successful start.", backupName))

	go func() {
		_ = exec.Command("docker", "image", "prune", "-f").Run()
	}()

	return nil
}

func (s *DockerContainers) setContainerUpToDate(id, name, image string) {
	if s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{json .RepoDigests}}", image)
	newDigest := ""
	if out, err := cmd.Output(); err == nil {
		var digests []string
		if json.Unmarshal(out, &digests) == nil && len(digests) > 0 {
			if idx := strings.Index(digests[0], "@"); idx != -1 {
				newDigest = digests[0][idx+1:]
			}
		}
	}

	_, _ = s.db.Exec(`
		INSERT INTO container_updates (container_id, container_name, image, current_digest, latest_digest, update_available, checked_at, error)
		VALUES (?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP, '')
		ON CONFLICT(container_id) DO UPDATE SET
			container_name = excluded.container_name,
			image = excluded.image,
			current_digest = excluded.current_digest,
			latest_digest = excluded.latest_digest,
			update_available = 0,
			checked_at = CURRENT_TIMESTAMP,
			error = ''
	`, id, name, image, newDigest, newDigest)
}

func (s *DockerContainers) loadAllUpdatesFromDB() map[string]UpdateCheckResult {
	res := make(map[string]UpdateCheckResult)
	if s.db == nil {
		return res
	}
	rows, err := s.db.Query(`SELECT container_id, container_name, image, current_digest, latest_digest, update_available, checked_at, error FROM container_updates`)
	if err != nil {
		return res
	}
	defer rows.Close()

	for rows.Next() {
		var u UpdateCheckResult
		var avail int
		if err := rows.Scan(&u.ContainerID, &u.ContainerName, &u.Image, &u.CurrentDigest, &u.LatestDigest, &avail, &u.CheckedAt, &u.Error); err == nil {
			u.UpdateAvailable = avail == 1
			if u.ContainerID != "" {
				res[u.ContainerID] = u
			}
			if u.ContainerName != "" {
				res[u.ContainerName] = u
			}
		}
	}
	return res
}

func (s *DockerContainers) getUpdateFromDB(idOrName string) (UpdateCheckResult, bool) {
	if s.db == nil {
		return UpdateCheckResult{}, false
	}
	var u UpdateCheckResult
	var avail int
	err := s.db.QueryRow(`
		SELECT container_id, container_name, image, current_digest, latest_digest, update_available, checked_at, error
		FROM container_updates WHERE container_id = ? OR container_name = ?
	`, idOrName, idOrName).Scan(&u.ContainerID, &u.ContainerName, &u.Image, &u.CurrentDigest, &u.LatestDigest, &avail, &u.CheckedAt, &u.Error)
	if err != nil {
		return UpdateCheckResult{}, false
	}
	u.UpdateAvailable = avail == 1
	return u, true
}

func (s *DockerContainers) saveUpdateToDB(u UpdateCheckResult) {
	if s.db == nil {
		return
	}
	avail := 0
	if u.UpdateAvailable {
		avail = 1
	}
	_, _ = s.db.Exec(`
		INSERT INTO container_updates (container_id, container_name, image, current_digest, latest_digest, update_available, checked_at, error)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)
		ON CONFLICT(container_id) DO UPDATE SET
			container_name = excluded.container_name,
			image = excluded.image,
			current_digest = excluded.current_digest,
			latest_digest = excluded.latest_digest,
			update_available = excluded.update_available,
			checked_at = CURRENT_TIMESTAMP,
			error = excluded.error
	`, u.ContainerID, u.ContainerName, u.Image, u.CurrentDigest, u.LatestDigest, avail, u.Error)
}

func parseImageRef(image string) (registry, repo, tag string) {
	tag = "latest"
	if idx := strings.LastIndex(image, ":"); idx != -1 {
		slashIdx := strings.LastIndex(image, "/")
		if idx > slashIdx {
			tag = image[idx+1:]
			image = image[:idx]
		}
	}

	parts := strings.Split(image, "/")
	if len(parts) == 1 {
		registry = "registry-1.docker.io"
		repo = "library/" + parts[0]
	} else if len(parts) == 2 {
		if strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") {
			registry = parts[0]
			repo = parts[1]
		} else {
			registry = "registry-1.docker.io"
			repo = parts[0] + "/" + parts[1]
		}
	} else {
		if strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") {
			registry = parts[0]
			repo = strings.Join(parts[1:], "/")
		} else {
			registry = "registry-1.docker.io"
			repo = strings.Join(parts, "/")
		}
	}
	return
}

func parseAuthHeader(header string) map[string]string {
	params := make(map[string]string)
	parts := strings.Split(header, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			k := strings.TrimSpace(kv[0])
			v := strings.Trim(strings.TrimSpace(kv[1]), `"`)
			params[k] = v
		}
	}
	return params
}

func fetchRemoteManifestDigest(ctx context.Context, registry, repo, tag string) (string, []string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", registry, repo, tag)

	req, err := http.NewRequestWithContext(ctx, "GET", manifestURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		authHeader := resp.Header.Get("Www-Authenticate")
		if strings.HasPrefix(authHeader, "Bearer ") {
			params := parseAuthHeader(authHeader[7:])
			realm := params["realm"]
			service := params["service"]
			scope := params["scope"]
			if scope == "" {
				scope = fmt.Sprintf("repository:%s:pull", repo)
			}

			tokenURL := fmt.Sprintf("%s?service=%s&scope=%s", realm, url.QueryEscape(service), url.QueryEscape(scope))
			tReq, tErr := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
			if tErr == nil {
				tResp, tErr := client.Do(tReq)
				if tErr == nil && tResp.StatusCode == http.StatusOK {
					var tokenData struct {
						Token       string `json:"token"`
						AccessToken string `json:"access_token"`
					}
					_ = json.NewDecoder(tResp.Body).Decode(&tokenData)
					tResp.Body.Close()

					tok := tokenData.Token
					if tok == "" {
						tok = tokenData.AccessToken
					}
					if tok != "" {
						req2, _ := http.NewRequestWithContext(ctx, "GET", manifestURL, nil)
						req2.Header.Set("Accept", "application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json")
						req2.Header.Set("Authorization", "Bearer "+tok)
						resp2, err2 := client.Do(req2)
						if err2 == nil {
							resp = resp2
							defer resp.Body.Close()
						}
					}
				}
			}
		}
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound {
		return "", nil, fmt.Errorf("public registry does not host this image (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}

	mainDigest := resp.Header.Get("Docker-Content-Digest")
	bodyBytes, _ := io.ReadAll(resp.Body)

	var subDigests []string
	var indexDoc struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(bodyBytes, &indexDoc); err == nil && len(indexDoc.Manifests) > 0 {
		for _, m := range indexDoc.Manifests {
			if m.Digest != "" {
				subDigests = append(subDigests, m.Digest)
			}
		}
	}

	return mainDigest, subDigests, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func isDocktunContainer(name, image string) bool {
	n := strings.ToLower(name)
	img := strings.ToLower(image)
	return n == "docktun" || n == "docktun-backend" || n == "docktun-frontend" ||
		strings.Contains(n, "docktun") ||
		img == "docktun" || img == "docktun:latest" ||
		strings.HasSuffix(img, "-docktun:latest") ||
		strings.Contains(img, "docktun")
}

func fetchLatestGitHubCommit(ctx context.Context, owner, repo, branch string) (string, string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", owner, repo, branch)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "DockTun-Dashboard/1.0")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	var data struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", "", err
	}

	shortSHA := data.SHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	firstLineMsg := strings.Split(data.Commit.Message, "\n")[0]
	if len(firstLineMsg) > 50 {
		firstLineMsg = firstLineMsg[:47] + "..."
	}
	return shortSHA, firstLineMsg, nil
}

func cleanPortNumber(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'[] `)
	s = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(s), "/tcp"), "/udp")
	s = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(s), "tcp/"), "udp/")
	s = strings.TrimSpace(s)
	if lastColon := strings.LastIndex(s, ":"); lastColon >= 0 {
		s = s[lastColon+1:]
	}
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 65535 {
		return strconv.Itoa(n)
	}
	return ""
}

func extractAllPortNumbers(s string) []string {
	var nums []string
	var current strings.Builder
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			current.WriteRune(ch)
		} else {
			if current.Len() > 0 {
				str := current.String()
				if n, err := strconv.Atoi(str); err == nil && n > 0 && n <= 65535 {
					nums = append(nums, str)
				}
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		str := current.String()
		if n, err := strconv.Atoi(str); err == nil && n > 0 && n <= 65535 {
			nums = append(nums, str)
		}
	}
	return nums
}

type ParsedPortMapping struct {
	HostIP        string
	HostPort      string
	ContainerPort string
	Protocol      string // "tcp" or "udp"
}

func (p ParsedPortMapping) FormatForCompose() string {
	proto := ""
	if p.Protocol == "udp" {
		proto = "/udp"
	}
	h := cleanPortNumber(p.HostPort)
	c := cleanPortNumber(p.ContainerPort)
	if c == "" {
		c = h
	}
	if h == "" {
		h = c
	}
	if h == "" && c == "" {
		return ""
	}

	ip := strings.Trim(p.HostIP, `"'[] `)
	if ip != "" && ip != "0.0.0.0" && ip != "::" && ip != ":::" && ip != "[::]" {
		if strings.Contains(ip, ":") && !strings.HasPrefix(ip, "[") {
			ip = "[" + ip + "]"
		}
		return fmt.Sprintf("%s:%s:%s%s", ip, h, c, proto)
	}
	return fmt.Sprintf("%s:%s%s", h, c, proto)
}

func (p ParsedPortMapping) FormatForDockerRun() string {
	return p.FormatForCompose()
}

func parsePortMapping(raw string) (ParsedPortMapping, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return ParsedPortMapping{}, false
	}

	proto := "tcp"
	if strings.Contains(strings.ToLower(raw), "/udp") {
		proto = "udp"
	}

	// Case 1: contains "->"
	if strings.Contains(raw, "->") {
		parts := strings.SplitN(raw, "->", 2)
		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])

		var hostSide, containerSide string
		if strings.Contains(left, "/tcp") || strings.Contains(left, "/udp") {
			containerSide = left
			hostSide = right
		} else if strings.Contains(right, "/tcp") || strings.Contains(right, "/udp") {
			containerSide = right
			hostSide = left
		} else if strings.Contains(left, ".") || strings.Contains(left, ":") {
			hostSide = left
			containerSide = right
		} else {
			hostSide = left
			containerSide = right
		}

		cPort := cleanPortNumber(containerSide)
		hostIP := ""
		hostPort := ""
		if lastColon := strings.LastIndex(hostSide, ":"); lastColon >= 0 {
			hostIP = strings.TrimSpace(hostSide[:lastColon])
			hostPort = cleanPortNumber(hostSide[lastColon+1:])
		} else {
			hostPort = cleanPortNumber(hostSide)
		}
		hostIP = strings.Trim(hostIP, "[]")

		if cPort != "" && hostPort != "" {
			return ParsedPortMapping{
				HostIP:        hostIP,
				HostPort:      hostPort,
				ContainerPort: cPort,
				Protocol:      proto,
			}, true
		} else if cPort != "" {
			return ParsedPortMapping{
				HostPort:      cPort,
				ContainerPort: cPort,
				Protocol:      proto,
			}, true
		}
	}

	// Case 2: Contains multiple colons or malformed IPv6, e.g. "7777/tcp:::7777" or ":::7777:7777" or "7777/tcp:::"
	if strings.Contains(raw, ":::") || strings.Contains(raw, "::::") {
		nums := extractAllPortNumbers(raw)
		if len(nums) >= 2 {
			return ParsedPortMapping{
				HostPort:      nums[0],
				ContainerPort: nums[1],
				Protocol:      proto,
			}, true
		} else if len(nums) == 1 {
			return ParsedPortMapping{
				HostPort:      nums[0],
				ContainerPort: nums[0],
				Protocol:      proto,
			}, true
		}
	}

	// Case 3: bracketed IPv6, e.g. "[::]:7777:7777" or "[::1]:7777:7777"
	if strings.HasPrefix(raw, "[") && strings.Contains(raw, "]:") {
		bracketEnd := strings.Index(raw, "]:")
		ip := raw[1:bracketEnd]
		rest := raw[bracketEnd+2:]
		restParts := strings.Split(rest, ":")
		if len(restParts) == 2 {
			hPort := cleanPortNumber(restParts[0])
			cPort := cleanPortNumber(restParts[1])
			if hPort != "" && cPort != "" {
				return ParsedPortMapping{
					HostIP:        ip,
					HostPort:      hPort,
					ContainerPort: cPort,
					Protocol:      proto,
				}, true
			}
		}
	}

	// Case 4: standard colon separated e.g. "host:container" or "ip:host:container"
	if strings.Contains(raw, ":") {
		colons := strings.Split(raw, ":")
		if len(colons) == 2 {
			part1 := strings.TrimSpace(colons[0])
			part2 := strings.TrimSpace(colons[1])

			// If part1 has /tcp or /udp, e.g. "7777/tcp:7777" -> part1 is container!
			if strings.Contains(strings.ToLower(part1), "/tcp") || strings.Contains(strings.ToLower(part1), "/udp") {
				c := cleanPortNumber(part1)
				h := cleanPortNumber(part2)
				if c != "" && h != "" {
					return ParsedPortMapping{
						HostPort:      h,
						ContainerPort: c,
						Protocol:      proto,
					}, true
				}
			}

			// Normal "host:container"
			h := cleanPortNumber(part1)
			c := cleanPortNumber(part2)
			if h != "" && c != "" {
				return ParsedPortMapping{
					HostPort:      h,
					ContainerPort: c,
					Protocol:      proto,
				}, true
			}
		} else if len(colons) == 3 {
			part1 := strings.TrimSpace(colons[0])
			part2 := strings.TrimSpace(colons[1])
			part3 := strings.TrimSpace(colons[2])

			if strings.Contains(strings.ToLower(part1), "/tcp") || strings.Contains(strings.ToLower(part1), "/udp") {
				c := cleanPortNumber(part1)
				h := cleanPortNumber(part3)
				if c != "" && h != "" {
					return ParsedPortMapping{
						HostPort:      h,
						ContainerPort: c,
						Protocol:      proto,
					}, true
				}
			}

			ip := strings.Trim(part1, "[]")
			h := cleanPortNumber(part2)
			c := cleanPortNumber(part3)
			if h != "" && c != "" {
				return ParsedPortMapping{
					HostIP:        ip,
					HostPort:      h,
					ContainerPort: c,
					Protocol:      proto,
				}, true
			}
		} else {
			// len(colons) > 3, e.g. multiple colons or IPv6 addresses
			nums := extractAllPortNumbers(raw)
			if len(nums) >= 2 {
				return ParsedPortMapping{
					HostPort:      nums[0],
					ContainerPort: nums[1],
					Protocol:      proto,
				}, true
			} else if len(nums) == 1 {
				return ParsedPortMapping{
					HostPort:      nums[0],
					ContainerPort: nums[0],
					Protocol:      proto,
				}, true
			}
		}
	}

	// Case 5: single port number
	clean := cleanPortNumber(raw)
	if clean != "" {
		return ParsedPortMapping{
			HostPort:      clean,
			ContainerPort: clean,
			Protocol:      proto,
		}, true
	}

	return ParsedPortMapping{}, false
}

func SanitizeComposeFile(composePath string) bool {
	contentBytes, err := os.ReadFile(composePath)
	if err != nil {
		return false
	}
	content := string(contentBytes)
	lines := strings.Split(content, "\n")
	modified := false
	inPorts := false
	seenPorts := make(map[string]bool)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ports:") {
			inPorts = true
			seenPorts = make(map[string]bool)
			continue
		}
		if inPorts {
			if strings.HasPrefix(trimmed, "-") {
				rawPort := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
				// Remove inline comment if present
				if cIdx := strings.Index(rawPort, "#"); cIdx != -1 {
					rawPort = strings.TrimSpace(rawPort[:cIdx])
				}
				rawPort = strings.Trim(rawPort, `"'`)

				parsed, ok := parsePortMapping(rawPort)
				if ok {
					correctCompose := parsed.FormatForCompose()
					if correctCompose != "" {
						if seenPorts[correctCompose] {
							dashIdx := strings.Index(line, "-")
							indent := line[:dashIdx]
							lines[i] = fmt.Sprintf("%s# duplicate port removed: %s", indent, rawPort)
							modified = true
							continue
						}
						seenPorts[correctCompose] = true

						if rawPort != correctCompose || strings.Contains(line, ":::") || strings.Contains(line, "/tcp::") {
							dashIdx := strings.Index(line, "-")
							indent := line[:dashIdx]
							lines[i] = fmt.Sprintf("%s- \"%s\"", indent, correctCompose)
							modified = true
						}
					}
				} else if strings.Contains(line, ":::") || strings.Contains(line, "/tcp:") {
					// Fallback for severely corrupted port line
					nums := extractAllPortNumbers(line)
					if len(nums) > 0 {
						hNum := nums[0]
						cNum := nums[len(nums)-1]
						proto := ""
						if strings.Contains(strings.ToLower(line), "udp") {
							proto = "/udp"
						}
						correct := fmt.Sprintf("%s:%s%s", hNum, cNum, proto)
						dashIdx := strings.Index(line, "-")
						indent := "      "
						if dashIdx >= 0 {
							indent = line[:dashIdx]
						}
						lines[i] = fmt.Sprintf("%s- \"%s\"", indent, correct)
						modified = true
					}
				}
			} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				inPorts = false
			}
		}
	}

	if modified {
		_ = os.WriteFile(composePath, []byte(strings.Join(lines, "\n")), 0644)
		log.Printf("[docktun] sanitized invalid ports in compose file: %s", composePath)
		return true
	}
	return false
}

func sanitizeComposeFile(composePath string) bool {
	return SanitizeComposeFile(composePath)
}

