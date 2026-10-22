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
	Mode        string `json:"mode"`
	Hostname    string `json:"hostname,omitempty"`
	Target      string `json:"target,omitempty"`
	PrivateType string `json:"private_type,omitempty"`
	PrivateHost string `json:"private_host,omitempty"`
	PrivatePort int    `json:"private_port,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

type ContainerMount struct {
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
	ProjectID              int              `json:"project_id,omitempty"`
	ProjectName            string           `json:"project_name,omitempty"`
	ProjectPath            string           `json:"project_path,omitempty"`
	ComposeFile            string           `json:"compose_file,omitempty"`
	Env                    []string         `json:"env,omitempty"`
	Mounts                 []ContainerMount `json:"mounts,omitempty"`
	IPAddress              string           `json:"ip_address,omitempty"`
	Gateway                string           `json:"gateway,omitempty"`
	MacAddress             string           `json:"mac_address,omitempty"`
	Networks               []string         `json:"networks,omitempty"`
	ExitCode               int              `json:"exit_code,omitempty"`
	ConnectedToCloudflared bool             `json:"connected_to_cloudflared"`
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

	statsMap := s.collectAllStats()
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
			c.ConnectedToCloudflared = strings.Contains(networksStr, "cloudflared")
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
			}
		}
		if c.ProjectID == 0 {
			if p, ok := projectsByName[strings.ToLower(c.Name)]; ok {
				c.ProjectID = p.ID
				c.ProjectName = p.Name
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
			Image   string            `json:"Image"`
			Cmd     []string          `json:"Cmd"`
			Env     []string          `json:"Env"`
			Labels  map[string]string `json:"Labels"`
		} `json:"Config"`
		Mounts []struct {
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
				IPAddress string `json:"IPAddress"`
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

	// Fetch live stats for single container
	sCtx, sCancel := context.WithTimeout(context.Background(), 3*time.Second)
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

	// Formatted ports
	ports := []string{}
	for port, bindings := range inspect.NetworkSettings.Ports {
		if len(bindings) == 0 {
			ports = append(ports, port)
			continue
		}
		for _, b := range bindings {
			if b.HostPort != "" {
				ports = append(ports, fmt.Sprintf("%s -> %s:%s", port, b.HostIP, b.HostPort))
			} else {
				ports = append(ports, fmt.Sprintf("%s -> %s", port, b.HostIP))
			}
		}
	}
	if len(ports) > 0 {
		c.Ports = strings.Join(ports, ", ")
	}

	// Exposure check
	ingressMap := s.loadCloudflareIngressMap()
	if ing, ok := ingressMap[c.Name]; ok {
		c.ExposureMode = "public"
		c.Hostname = ing.Hostname
		c.Target = ing.Service
		c.Exposure = ExposureSettings{Mode: "public", Hostname: ing.Hostname, Target: ing.Service}
	} else {
		exp := s.getExposureFromDB(c.Name)
		c.ExposureMode = exp.Mode
		c.Hostname = exp.Hostname
		c.Target = exp.Target
		c.PrivateType = exp.PrivateType
		c.PrivateHost = exp.PrivateHost
		c.PrivatePort = exp.PrivatePort
		c.Notes = exp.Notes
		c.Exposure = exp
	}

	// Mounts
	mounts := make([]ContainerMount, 0, len(inspect.Mounts))
	for _, m := range inspect.Mounts {
		mounts = append(mounts, ContainerMount{
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
	detail.Mounts = mounts
	detail.IPAddress = inspect.NetworkSettings.IPAddress
	detail.Gateway = inspect.NetworkSettings.Gateway
	detail.MacAddress = inspect.NetworkSettings.MacAddress
	detail.Networks = nets
	detail.ExitCode = inspect.State.ExitCode

	_, hasCloudflared := inspect.NetworkSettings.Networks["cloudflared"]
	detail.ConnectedToCloudflared = hasCloudflared
	detail.Container.ConnectedToCloudflared = hasCloudflared

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

func (s *DockerContainers) ConnectNetwork(id string, network string) error {
	if network == "" {
		network = "cloudflared"
	}
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
		network = "cloudflared"
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
	if detail.ProjectID > 0 {
		return detail.ProjectID, nil
	}

	cname := strings.TrimPrefix(detail.Name, "/")
	if cname == "" {
		cname = id
	}

	destDir := filepath.Join("/opt/projects", cname)
	if s.cfg.SelfPath != "" {
		destDir = filepath.Join(filepath.Dir(s.cfg.SelfPath), cname)
	}

	_ = os.MkdirAll(destDir, 0755)

	composePath := filepath.Join(destDir, "docker-compose.yml")
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		img := detail.Image
		if img == "" {
			img = "alpine"
		}
		var b strings.Builder
		b.WriteString("services:\n")
		b.WriteString(fmt.Sprintf("  %s:\n", cname))
		b.WriteString(fmt.Sprintf("    image: %s\n", img))
		b.WriteString(fmt.Sprintf("    container_name: %s\n", cname))
		b.WriteString("    restart: unless-stopped\n")
		if detail.Command != "" {
			b.WriteString(fmt.Sprintf("    command: %s\n", detail.Command))
		}
		if len(detail.Mounts) > 0 {
			b.WriteString("    volumes:\n")
			for _, m := range detail.Mounts {
				if m.Source != "" && m.Destination != "" {
					mode := "rw"
					if !m.RW {
						mode = "ro"
					}
					b.WriteString(fmt.Sprintf("      - %s:%s:%s\n", m.Source, m.Destination, mode))
				}
			}
		}
		if len(detail.Env) > 0 {
			b.WriteString("    environment:\n")
			for _, e := range detail.Env {
				if !strings.Contains(e, "********") && !strings.HasPrefix(e, "PATH=") {
					b.WriteString(fmt.Sprintf("      - %s\n", e))
				}
			}
		}
		if detail.Ports != "" {
			var portLines []string
			seen := make(map[string]bool)
			for _, part := range strings.Split(detail.Ports, ",") {
				part = strings.TrimSpace(part)
				if idx := strings.Index(part, "->"); idx > 0 {
					hostPart := strings.TrimSpace(part[:idx])
					containerPart := strings.TrimSpace(part[idx+2:])
					if hIdx := strings.LastIndex(hostPart, ":"); hIdx >= 0 {
						hostPart = hostPart[hIdx+1:]
					}
					cPort := strings.TrimSuffix(containerPart, "/tcp")
					mapping := fmt.Sprintf("%s:%s", hostPart, cPort)
					if !seen[mapping] && hostPart != "" && cPort != "" {
						seen[mapping] = true
						portLines = append(portLines, fmt.Sprintf("      - \"%s\"\n", mapping))
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

		if detail.ExposureMode == "public" {
			b.WriteString("    networks:\n")
			b.WriteString("      - default\n")
			b.WriteString("      - cloudflared\n\n")
			b.WriteString("networks:\n")
			b.WriteString("  cloudflared:\n")
			b.WriteString("    external: true\n")
		}

		_ = os.WriteFile(composePath, []byte(b.String()), 0644)
	}

	envPath := filepath.Join(destDir, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		_ = os.WriteFile(envPath, []byte("# Environment variables for "+cname+"\n"), 0644)
	}

	if s.db != nil {
		res, err := s.db.Exec(`INSERT OR IGNORE INTO projects (name, path) VALUES (?, ?)`, cname, destDir)
		if err != nil {
			return 0, err
		}
		var projID int
		_ = s.db.QueryRow(`SELECT id FROM projects WHERE path = ?`, destDir).Scan(&projID)
		if projID == 0 {
			lastID, _ := res.LastInsertId()
			projID = int(lastID)
		}
		return projID, nil
	}

	return 0, fmt.Errorf("database unavailable to register project")
}

func (s *DockerContainers) Start(id string) error { return dockerCommand("start", id) }
func (s *DockerContainers) Stop(id string) error { return dockerCommand("stop", id) }
func (s *DockerContainers) Restart(id string) error { return dockerCommand("restart", id) }

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

	// 1. If public mode, connect container to cloudflared network; otherwise disconnect
	if settings.Mode == "public" {
		if err := s.ConnectNetwork(name, "cloudflared"); err != nil {
			log.Printf("[docktun] connect network cloudflared for %s: %v", name, err)
		}
	} else {
		_ = s.DisconnectNetwork(name, "cloudflared")
	}

	// 2. Update Cloudflare ingress if mode is public or if changing away from public
	if s.configMgr != nil {
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
	return s.saveExposureToDB(name, cleanSettings)
}

func (s *DockerContainers) syncCloudflareIngress(containerName string, settings ExposureSettings) error {
	raw, err := s.configMgr.Read()
	if err != nil {
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

	rows, err := s.db.Query(`SELECT container_name, mode, hostname, target, private_type, private_host, private_port, notes FROM container_exposures`)
	if err != nil {
		return res
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var exp ExposureSettings
		var pPort sql.NullInt64
		if err := rows.Scan(&name, &exp.Mode, &exp.Hostname, &exp.Target, &exp.PrivateType, &exp.PrivateHost, &pPort, &exp.Notes); err == nil {
			if pPort.Valid {
				exp.PrivatePort = int(pPort.Int64)
			}
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
	err := s.db.QueryRow(
		`SELECT mode, hostname, target, private_type, private_host, private_port, notes FROM container_exposures WHERE container_name = ?`,
		name,
	).Scan(&exp.Mode, &exp.Hostname, &exp.Target, &exp.PrivateType, &exp.PrivateHost, &pPort, &exp.Notes)
	if err != nil {
		return ExposureSettings{Mode: "internal"}
	}
	if pPort.Valid {
		exp.PrivatePort = int(pPort.Int64)
	}
	return exp
}

func (s *DockerContainers) saveExposureToDB(name string, exp ExposureSettings) error {
	if s.db == nil {
		return nil
	}

	_, err := s.db.Exec(`
		INSERT INTO container_exposures (container_name, mode, hostname, target, private_type, private_host, private_port, notes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(container_name) DO UPDATE SET
			mode = excluded.mode,
			hostname = excluded.hostname,
			target = excluded.target,
			private_type = excluded.private_type,
			private_host = excluded.private_host,
			private_port = excluded.private_port,
			notes = excluded.notes,
			updated_at = CURRENT_TIMESTAMP
	`, name, exp.Mode, exp.Hostname, exp.Target, exp.PrivateType, exp.PrivateHost, exp.PrivatePort, exp.Notes)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "system", "prune", "-f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker prune failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
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

	res := UpdateCheckResult{
		ContainerID:   detail.ID,
		ContainerName: detail.Name,
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
		res.Error = fmt.Sprintf("unable to check registry: %v", err)
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

	cname := detail.Name
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

	// Check if compose managed
	compDir := ""
	compFile := ""
	compService := ""

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspectCmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .Config.Labels}}", id)
	if out, err := inspectCmd.Output(); err == nil {
		var labels map[string]string
		if err := json.Unmarshal(out, &labels); err == nil {
			compDir = labels["com.docker.compose.project.working_dir"]
			compFile = labels["com.docker.compose.project.config_files"]
			compService = labels["com.docker.compose.service"]
		}
	}

	if compService == "" {
		compService = cname
	}

	if compDir != "" {
		if _, err := os.Stat(compDir); err == nil {
			appendLog(fmt.Sprintf("[STEP 2/4] Updating via Docker Compose in %s (service: %s)...", compDir, compService))
			var composeArgs []string
			if compFile != "" {
				composeArgs = append(composeArgs, "-f", compFile)
			} else {
				composeArgs = append(composeArgs, "--project-directory", compDir)
			}
			composeArgs = append(composeArgs, "up", "-d", "--no-deps", compService)

			upCmd := exec.Command("docker", append([]string{"compose"}, composeArgs...)...)
			upCmd.Dir = compDir
			upOut, upErr := upCmd.CombinedOutput()
			appendLog(strings.TrimSpace(string(upOut)))
			if upErr != nil {
				return logOutput.String(), fmt.Errorf("docker compose up failed: %s (%w)", strings.TrimSpace(string(upOut)), upErr)
			}
		} else {
			compDir = ""
		}
	}

	if compDir == "" {
		appendLog(fmt.Sprintf("[STEP 2/4] Standalone container detected. Scaffolding compose configuration..."))
		_, projErr := s.EnsureProjectForContainer(id)
		if projErr != nil {
			return logOutput.String(), fmt.Errorf("failed to scaffold compose project for %s: %w", cname, projErr)
		}

		destDir := filepath.Join("/opt/projects", cname)
		if s.cfg.SelfPath != "" {
			destDir = filepath.Join(filepath.Dir(s.cfg.SelfPath), cname)
		}
		composeFilePath := filepath.Join(destDir, "docker-compose.yml")

		appendLog(fmt.Sprintf("[STEP 3/4] Stopping and removing old container %s...", cname))
		_ = exec.Command("docker", "stop", id).Run()
		_ = exec.Command("docker", "rm", id).Run()

		appendLog(fmt.Sprintf("[STEP 4/4] Starting updated container with new image..."))
		upCmd := exec.Command("docker", "compose", "-f", composeFilePath, "up", "-d")
		upCmd.Dir = destDir
		upOut, upErr := upCmd.CombinedOutput()
		appendLog(strings.TrimSpace(string(upOut)))
		if upErr != nil {
			return logOutput.String(), fmt.Errorf("failed to recreate container: %s (%w)", strings.TrimSpace(string(upOut)), upErr)
		}
	}

	if detail.ExposureMode == "public" {
		appendLog("[NETWORK] Ensuring connection to cloudflared network...")
		_ = s.ConnectNetwork(cname, "cloudflared")
	}

	s.setContainerUpToDate(id, cname, image)

	if s.audit != nil {
		s.audit.Log(userID, "container_update", fmt.Sprintf("Updated container %s (%s) to latest image", cname, image), ip)
	}

	appendLog(fmt.Sprintf("✔ [SUCCESS] Container %s updated and running successfully!", cname))
	return logOutput.String(), nil
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
