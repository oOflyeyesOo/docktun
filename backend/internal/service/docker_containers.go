package service

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"docktun/internal/config"
	"docktun/internal/db"
	"gopkg.in/yaml.v3"
)

type ExposureSettings struct {
	cleanup                  *tailscaleRuleCleanup
	previousCloudflareHost   string
	previousCloudflareTarget string
	TailscaleNode            string `json:"tailscale_node,omitempty"`
	Mode                     string `json:"mode"`
	Hostname                 string `json:"hostname,omitempty"`
	Target                   string `json:"target,omitempty"`
	PrivateType              string `json:"private_type,omitempty"`
	PrivateHost              string `json:"private_host,omitempty"`
	PrivatePort              int    `json:"private_port,omitempty"`
	Notes                    string `json:"notes,omitempty"`
	ZeroTrustEnabled         bool   `json:"zero_trust_enabled,omitempty"`
	ZeroTrustEmails          string `json:"zero_trust_emails,omitempty"`
	ZeroTrustAuthType        string `json:"zero_trust_auth_type,omitempty"`
	ZeroTrustSession         string `json:"zero_trust_session,omitempty"`
	ZeroTrustAppID           string `json:"zero_trust_app_id,omitempty"`
}

func (e *ExposureSettings) UnmarshalJSON(data []byte) error {
	type Alias ExposureSettings
	aux := struct {
		PrivatePort interface{} `json:"private_port,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(e),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	switch v := aux.PrivatePort.(type) {
	case float64:
		e.PrivatePort = int(v)
	case int:
		e.PrivatePort = v
	case string:
		v = strings.TrimSpace(v)
		if v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				e.PrivatePort = p
			}
		}
	}
	return nil
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
	ComposeProject         string           `json:"compose_project,omitempty"`
	ComposeService         string           `json:"compose_service,omitempty"`
	ComposeWorkingDir      string           `json:"compose_working_dir,omitempty"`
	ComposeConfigFiles     string           `json:"compose_config_files,omitempty"`
	ComposeSources         []string         `json:"compose_sources,omitempty"`
	ComposeSourceError     string           `json:"compose_source_error,omitempty"`
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
	NetworkMode            string              `json:"network_mode,omitempty"`
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
	cfg            *config.Config
	db             *db.DB
	configMgr      *ConfigManager
	cloudflared    *Cloudflared
	cfAPI          *CloudflareAPI
	audit          *AuditLogger
	updateMu       sync.RWMutex
	statsMu        sync.RWMutex
	statsCache     map[string]liveStats
	statsTime      time.Time
	statsRunning   int32
	statsCollector func() map[string]liveStats
	selfUpdater    *SelfUpdater
	exposureMu     sync.Mutex
	exposureDocker func(context.Context, ...string) ([]byte, error)
	networkDocker  func(context.Context, ...string) ([]byte, error)
}

// Only completed, explicitly labelled helpers may be removed. A long pull is not a zombie.
func CleanupZombieUpdaters() {
	out, err := exec.Command("docker", "ps", "-aq", "--filter", "label=docktun.update.target", "--filter", "status=exited").Output()
	if err == nil {
		for _, id := range strings.Fields(string(out)) {
			_ = exec.Command("docker", "rm", id).Run()
		}
	}
}

func NewDockerContainers(cfg *config.Config, db *db.DB, configMgr *ConfigManager, cloudflared *Cloudflared, cfAPI *CloudflareAPI, audit *AuditLogger) *DockerContainers {
	go CleanupZombieUpdaters()
	service := &DockerContainers{
		selfUpdater: NewSelfUpdater(filepath.Dir(cfg.DBPath)),
		cfg:         cfg,
		db:          db,
		configMgr:   configMgr,
		cloudflared: cloudflared,
		cfAPI:       cfAPI,
		audit:       audit,
	}
	go service.getCachedStats()
	return service
}

type liveStats struct {
	cpu   string
	mem   string
	net   string
	block string
	pids  int
}

func (s *DockerContainers) List() ([]Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.State}}\t{{.Ports}}\t{{.RunningFor}}\t{{.Command}}\t{{.Networks}}\t{{or (.Label \"docktun.update.target\") (.Label \"docktun.storage-check\")}}\t{{.Label \"com.docker.compose.project\"}}\t{{.Label \"com.docker.compose.project.working_dir\"}}\t{{.Label \"com.docker.compose.service\"}}\t{{.Label \"com.docker.compose.project.config_files\"}}")
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
	projectIndex := s.loadContainerProjects()
	cachedUpdates := s.loadAllUpdatesFromDB()
	exposureNetwork := s.exposureNetworkName()

	rows := strings.Split(text, "\n")
	containers := make([]Container, 0, len(rows))

	for _, row := range rows {
		if strings.TrimSpace(row) == "" {
			continue
		}

		fields := strings.SplitN(row, "\t", 14)
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
			c.ConnectedToCloudflared = exactNetworkMembership(networksStr, exposureNetwork)
		}

		if len(fields) >= 10 && strings.TrimSpace(fields[9]) != "" {
			continue
		}
		labels := map[string]string{}
		for offset, key := range []string{"com.docker.compose.project", "com.docker.compose.project.working_dir", "com.docker.compose.service", "com.docker.compose.project.config_files"} {
			if len(fields) > 10+offset {
				labels[key] = strings.TrimSpace(fields[10+offset])
			}
		}
		s.assignContainerProject(&c, labels, projectIndex)

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

		ingress, found := ingressMap[c.Name]
		setContainerExposure(&c, projectExposure(dbExposures[c.Name], ingress, found))

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
	sampledAt := s.statsTime
	cached := make(map[string]liveStats, len(s.statsCache))
	for name, value := range s.statsCache {
		cached[name] = value
	}
	s.statsMu.RUnlock()
	// An empty sample is valid too (or a failed daemon probe); do not retry on
	// every HTTP request when there are no samples to return yet.
	if !sampledAt.IsZero() && time.Since(sampledAt) < 6*time.Second {
		return cached
	}
	if atomic.CompareAndSwapInt32(&s.statsRunning, 0, 1) {
		go func() {
			defer atomic.StoreInt32(&s.statsRunning, 0)
			collect := s.statsCollector
			if collect == nil {
				collect = s.collectAllStats
			}
			fresh := collect()
			s.statsMu.Lock()
			s.statsCache = fresh
			s.statsTime = time.Now()
			s.statsMu.Unlock()
		}()
	}
	// Never block inventory or inspection on Docker's multi-second CPU sample.
	// Missing readings stay unavailable until the next sample arrives.
	return cached
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
			Privileged  bool     `json:"Privileged"`
			CapAdd      []string `json:"CapAdd"`
			NetworkMode string   `json:"NetworkMode"`
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

	// Use the shared background sample; stopped containers have no live usage.
	statsMap := s.getCachedStats()
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

	ingress, found := s.loadCloudflareIngressMap()[c.Name]
	setContainerExposure(&c, projectExposure(s.getExposureFromDB(c.Name), ingress, found))

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
	detail.NetworkMode = inspect.HostConfig.NetworkMode
	if detail.NetworkMode == "" {
		for _, n := range nets {
			if n == "host" {
				detail.NetworkMode = "host"
				break
			}
		}
	}
	detail.NetworkAliases = make(map[string][]string)
	for netName, netInfo := range inspect.NetworkSettings.Networks {
		if len(netInfo.Aliases) > 0 {
			detail.NetworkAliases[netName] = netInfo.Aliases
		}
	}

	_, connected := inspect.NetworkSettings.Networks[s.exposureNetworkName()]
	detail.ConnectedToCloudflared = connected
	detail.Container.ConnectedToCloudflared = connected

	compFile := inspect.Config.Labels["com.docker.compose.project.config_files"]
	detail.ComposeFile = compFile

	projectIndex := s.loadContainerProjects()
	s.assignContainerProject(&detail.Container, inspect.Config.Labels, projectIndex)
	detail.ProjectID = detail.Container.ProjectID
	detail.ProjectName = detail.Container.ProjectName
	detail.ProjectPath = detail.Container.ProjectPath

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

func (s *DockerContainers) isSelfContainer(id string, _ string) bool {
	if host, err := os.Hostname(); err == nil && len(host) >= 12 && buildRevisionPattern.MatchString(host) && len(id) >= 12 && (strings.HasPrefix(id, host) || strings.HasPrefix(host, id)) {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	own, err := selfContainer(ctx)
	return err == nil && len(id) >= 12 && (own.ID == id || strings.HasPrefix(own.ID, id))
}

func (s *DockerContainers) ReconcileNetworks() (map[string]interface{}, error) {
	result := make(map[string]interface{})
	var logs []string
	addLog := func(msg string) {
		log.Printf("[network-reconcile] %s", msg)
		logs = append(logs, msg)
	}

	defNet := s.exposureNetworkName()
	if err := s.EnsureNetwork(defNet); err != nil {
		return nil, fmt.Errorf("resolve primary network: %w", err)
	}
	addLog(fmt.Sprintf("Primary network: %s", defNet))

	networksToBridge := []string{defNet}

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

	// Only public apps join the Cloudflare networks. Private apps have independent networking.
	if s.db != nil {
		rows, err := s.db.Query(`SELECT container_name FROM container_exposures WHERE mode = 'public'`)
		if err == nil && rows != nil {
			defer rows.Close()
			for rows.Next() {
				var cName string
				if err := rows.Scan(&cName); err == nil && cName != "" {
					for _, net := range networksToBridge {
						_ = s.ConnectNetwork(cName, net)
					}
					_ = s.EnsureDatabaseLinks(cName)
					addLog(fmt.Sprintf("Verified network connectivity and database links for %s", cName))
				}
			}
		}
	}

	// 4b. Auto-heal any containers currently crashing or restarting
	restartingCmd := exec.Command("docker", "ps", "-a", "--filter", "status=restarting", "--format", "{{.Names}}")
	if rOut, err := restartingCmd.Output(); err == nil {
		for _, name := range strings.Split(string(rOut), "\n") {
			name = strings.TrimSpace(name)
			if name != "" && !strings.HasPrefix(name, "docktun-updater-") {
				_ = s.EnsureDatabaseLinks(name)
				addLog(fmt.Sprintf("Checked configured dependency networks for restarting container %s", name))
			}
		}
	}

	// Network attachment does not require restarting the tunnel. Periodic
	// reconciliation must not interrupt existing connections every ten minutes.

	// 6. Automatically sync/reconcile Cloudflare DNS records for all public ingress routes
	if s.cfAPI != nil && s.configMgr != nil && s.cloudflared != nil && s.cloudflared.Status().Status != "disabled" {
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
	_, err := s.EnsureNetworkID(network)
	return err
}

func (s *DockerContainers) EnsureNetworkID(network string) (string, error) {
	if network == "" {
		network = s.DefaultNetwork()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	providers := NewProviderContainers(s.cfg)
	if s.networkDocker != nil {
		providers.run = s.networkDocker
	}
	return providers.resolveNetwork(ctx, network)
}

func (s *DockerContainers) ConnectNetwork(id string, network string) error {
	if network == "" {
		network = s.DefaultNetwork()
	}
	netTarget, err := s.EnsureNetworkID(network)
	if err != nil {
		return err
	}
	cmd := exec.Command("docker", "network", "connect", netTarget, id)
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

	// Determine canonical Stack / Project Name
	targetProjectName := strings.TrimSpace(detail.ProjectName)
	targetProjectPath := strings.TrimSpace(detail.ProjectPath)

	if s.isSelfContainer(detail.ID, cname) {
		return 0, fmt.Errorf("manage DockTun through Settings > DockTun services")
	}

	if detail.ProjectID > 0 {
		return detail.ProjectID, nil
	}
	if detail.ComposeProject != "" {
		return 0, fmt.Errorf("Compose stack %q has no registered source folder; configure its actual stack folder instead of importing a single container", detail.ComposeProject)
	}
	if targetProjectName == "" {
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
		isHostNet := detail.NetworkMode == "host"
		if !isHostNet {
			for _, net := range detail.Networks {
				if net == "host" {
					isHostNet = true
					break
				}
			}
		}

		var b strings.Builder
		b.WriteString("services:\n")
		b.WriteString(fmt.Sprintf("  %s:\n", cname))
		b.WriteString(fmt.Sprintf("    image: %s\n", img))
		b.WriteString(fmt.Sprintf("    container_name: %s\n", cname))
		if isHostNet {
			b.WriteString("    network_mode: host\n")
		}
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
		if !isHostNet && detail.Ports != "" {
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

		if !isHostNet && len(svcNetworks) > 0 {
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

func (s *DockerContainers) Start(id string) error {
	_ = s.EnsureDatabaseLinks(id)
	s.exposureMu.Lock()
	defer s.exposureMu.Unlock()
	if err := dockerCommand("start", id); err != nil {
		return err
	}
	return s.finishTailscaleStart(id)
}
func (s *DockerContainers) Stop(id string) error { return dockerCommand("stop", id) }
func (s *DockerContainers) Restart(id string) error {
	_ = s.EnsureDatabaseLinks(id)
	s.exposureMu.Lock()
	defer s.exposureMu.Unlock()
	if err := dockerCommand("restart", id); err != nil {
		return err
	}
	return s.finishTailscaleStart(id)
}

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

	ingress, found := s.loadCloudflareIngressMap()[name]
	return projectExposure(s.getExposureFromDB(name), ingress, found), nil
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
	s.exposureMu.Lock()
	defer s.exposureMu.Unlock()
	name, err := s.containerName(id)
	if err != nil {
		return err
	}

	if settings.Mode == "" {
		settings.Mode = "internal"
	}

	if settings.Mode != "internal" && settings.Mode != "public" && settings.Mode != "private" {
		return fmt.Errorf("choose internal, public, or private access")
	}
	if settings.Mode == "private" && settings.PrivateType != "tailscale" && settings.PrivateType != "wireguard" && settings.PrivateType != "mesh" {
		return fmt.Errorf("choose a private access provider")
	}
	if settings.Mode == "private" && settings.PrivateType != "tailscale" && (strings.TrimSpace(settings.PrivateHost) == "" || settings.PrivatePort < 1 || settings.PrivatePort > 65535) {
		return fmt.Errorf("enter the existing VPN address and port; WireGuard/custom mesh routing must already be configured")
	}
	if settings.Mode == "public" {
		if cleanHostname(settings.Hostname) == "" {
			return fmt.Errorf("enter the public hostname to expose")
		}
		if err := s.validateCloudflarePublish(); err != nil {
			return err
		}
	}

	prevExp := s.getExposureFromDB(name)
	ingress, err := s.exposureIngressForMutation(settings.Mode)
	if err != nil {
		return err
	}
	actual, actualFound := ingress[name]
	if actualFound {
		settings.previousCloudflareHost, settings.previousCloudflareTarget = actual.Hostname, actual.Service
	}
	undo, err := s.syncTailscaleExposure(name, prevExp, &settings)
	if err != nil {
		return err
	}
	committed := false
	undoNetwork := func() {}
	defer func() {
		if !committed {
			undoNetwork()
			undo()
		}
	}()
	if settings.Mode == "public" {
		undoNetwork, err = s.connectExposureNetwork(name)
		if err != nil {
			return fmt.Errorf("connect Cloudflare app network: %w", err)
		}
	}

	// 2. Update Cloudflare ingress ONLY if currently public or was previously public
	if s.configMgr != nil && (settings.Mode == "public" || prevExp.Mode == "public" || actualFound) {
		if err := s.syncCloudflareIngress(name, settings); err != nil {
			var partial *cloudflareDNSPartialError
			if errors.As(err, &partial) {
				// The accepted route already needs this endpoint. Keep it while
				// reporting DNS failure and restoring the previous saved provider.
				undoNetwork = func() {}
			}
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

	committed = true
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

	return nil
}

func (s *DockerContainers) syncCloudflareIngress(containerName string, settings ExposureSettings) error {
	if s.configMgr == nil {
		return nil
	}

	identity, identityErr := s.configMgr.TunnelIdentity()
	if identity.Remote {
		if identityErr != nil {
			return identityErr
		}
		return s.syncRemoteCloudflareIngress(containerName, settings, identity)
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

	var rawMap map[string]interface{}
	_ = yaml.Unmarshal([]byte(raw), &rawMap)
	if rawMap == nil {
		rawMap = make(map[string]interface{})
	}

	cleanHost := cleanHostname(settings.Hostname)

	// Filter out any existing rule for this container or hostname, preserving originRequest (e.g. httpHostHeader)
	var existingOriginReq *cfOriginRequest
	newIngress := make([]cfIngressRule, 0, len(doc.Ingress)+1)
	for _, rule := range doc.Ingress {
		// Ignore catch-all rule during filter
		if rule.Service == "http_status:404" {
			continue
		}
		// If rule matches container name or hostname
		if strings.Contains(rule.Service, containerName) || (cleanHost != "" && cleanHostname(rule.Hostname) == cleanHost) {
			if rule.OriginRequest != nil {
				existingOriginReq = rule.OriginRequest
			}
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
		if existingOriginReq != nil {
			rule.OriginRequest = existingOriginReq
		} else if strings.HasPrefix(target, "https://") {
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
	rawMap["ingress"] = newIngress

	updatedYaml, err := yaml.Marshal(rawMap)
	if err != nil {
		// Fallback to typed doc if rawMap fails
		updatedYaml, err = yaml.Marshal(&doc)
		if err != nil {
			return fmt.Errorf("marshal updated cloudflared config: %w", err)
		}
	}

	if err := s.configMgr.Write(string(updatedYaml), 1); err != nil {
		return err
	}

	// Reload an enabled connector; editing an app never enables a stopped provider.
	if s.cloudflared != nil && s.cloudflared.IsRunning() {
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
	identity, identityErr := s.configMgr.TunnelIdentity()
	if identity.Remote {
		if identityErr == nil && s.cfAPI != nil {
			return s.cfAPI.cachedTunnelIngress(identity)
		}
		return result
	}
	if identityErr != nil {
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
		if rule.Hostname == "" || rule.Path != "" {
			continue
		}
		hostPart := ingressContainer(rule.Service)
		if hostPart != "" {
			if _, found := result[hostPart]; !found {
				result[hostPart] = rule
			}
		}
	}

	return result
}

func (s *DockerContainers) loadAllExposuresFromDB() map[string]ExposureSettings {
	res := make(map[string]ExposureSettings)
	if s.db == nil {
		return res
	}

	rows, err := s.db.Query(`SELECT container_name, mode, hostname, target, private_type, private_host, private_port, notes, COALESCE(zero_trust_enabled, 0), COALESCE(zero_trust_emails, ''), COALESCE(zero_trust_app_id, ''), COALESCE(zero_trust_auth_type, 'email'), COALESCE(zero_trust_session, '24h'), COALESCE(tailscale_node, '') FROM container_exposures`)
	if err != nil {
		return res
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var exp ExposureSettings
		var pPort sql.NullInt64
		var ztInt int
		if err := rows.Scan(&name, &exp.Mode, &exp.Hostname, &exp.Target, &exp.PrivateType, &exp.PrivateHost, &pPort, &exp.Notes, &ztInt, &exp.ZeroTrustEmails, &exp.ZeroTrustAppID, &exp.ZeroTrustAuthType, &exp.ZeroTrustSession, &exp.TailscaleNode); err == nil {
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
		`SELECT mode, hostname, target, private_type, private_host, private_port, notes, COALESCE(zero_trust_enabled, 0), COALESCE(zero_trust_emails, ''), COALESCE(zero_trust_app_id, ''), COALESCE(zero_trust_auth_type, 'email'), COALESCE(zero_trust_session, '24h'), COALESCE(tailscale_node, '') FROM container_exposures WHERE container_name = ?`,
		name,
	).Scan(&exp.Mode, &exp.Hostname, &exp.Target, &exp.PrivateType, &exp.PrivateHost, &pPort, &exp.Notes, &ztInt, &exp.ZeroTrustEmails, &exp.ZeroTrustAppID, &exp.ZeroTrustAuthType, &exp.ZeroTrustSession, &exp.TailscaleNode)
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

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
		INSERT INTO container_exposures (container_name, mode, hostname, target, private_type, private_host, private_port, notes, zero_trust_enabled, zero_trust_emails, zero_trust_app_id, zero_trust_auth_type, zero_trust_session, tailscale_node, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
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
			tailscale_node = excluded.tailscale_node,
			updated_at = CURRENT_TIMESTAMP
	`, name, exp.Mode, exp.Hostname, exp.Target, exp.PrivateType, exp.PrivateHost, exp.PrivatePort, exp.Notes, ztInt, exp.ZeroTrustEmails, exp.ZeroTrustAppID, exp.ZeroTrustAuthType, exp.ZeroTrustSession, exp.TailscaleNode)
	if err != nil {
		return err
	}
	// A stopped helper cannot run the CLI. Record its exact old rule in the
	// same transaction as the new exposure, for cleanup on explicit re-enable.
	if pending := exp.cleanup; pending != nil {
		if _, err = tx.Exec(`INSERT INTO tailscale_rule_cleanup (app, node, port, target) VALUES (?, ?, ?, ?)
			ON CONFLICT(app, node, port) DO UPDATE SET target = excluded.target`, name, pending.Node, pending.Port, pending.Target); err != nil {
			return err
		}
	}
	return tx.Commit()
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

	var out []byte
	var err error
	if s.exposureDocker != nil {
		out, err = s.exposureDocker(ctx, "inspect", "--format", "{{.Name}}", id)
	} else {
		out, err = exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Name}}", id).Output()
	}
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
	timeout := 10 * time.Second
	if len(args) > 0 {
		switch args[0] {
		case "start", "stop", "restart":
			// Docker stop/restart honor the container's graceful shutdown period.
			// The client must outlive the default ten-second grace plus cleanup.
			timeout = 2 * time.Minute
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("docker %s failed: %s (%w)", strings.Join(args, " "), detail, err)
		}
		return fmt.Errorf("docker %s failed: %w", strings.Join(args, " "), err)
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

type OriginProbeOptions struct {
	NoTLSVerify      bool   `json:"noTLSVerify"`
	HTTPHostHeader   string `json:"httpHostHeader"`
	OriginServerName string `json:"originServerName"`
}

func (s *DockerContainers) ProbeTarget(targetURL string) (map[string]interface{}, error) {
	return s.ProbeTargetWithOptions(targetURL, OriginProbeOptions{})
}

func (s *DockerContainers) ProbeTargetWithOptions(targetURL string, options OriginProbeOptions) (map[string]interface{}, error) {
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
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		result["error"] = "Use an HTTP or HTTPS origin URL with a hostname"
		return result, nil
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: options.NoTLSVerify, ServerName: options.OriginServerName} // Only disabled by an explicit origin option.
	defer transport.CloseIdleConnections()
	request, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		result["error"] = err.Error()
		return result, nil
	}
	if options.HTTPHostHeader != "" {
		request.Host = options.HTTPHostHeader
	}
	start := time.Now()
	client := &http.Client{
		Timeout:   4 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(request)
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

	if s.isSelfContainer(detail.ID, cname) && s.selfUpdater != nil {
		state := s.selfUpdater.Status()
		res.UpdateAvailable = state.Available
		res.CurrentDigest = state.CurrentImage
		res.Image = state.Image
		res.Error = "Use Settings > Update DockTun to check and install published updates."
		s.saveUpdateToDB(res)
		return res, nil
	}
	// 0. Detect local DockTun image tag (e.g. "docktun:latest" or "docktun" without remote registry repo)
	if isLocalDocktunImage(detail.Image) {
		localCommit := getLocalGitCommit(s.cfg.SelfPath, detail)
		commitSHA, commitMsg, ghErr := fetchLatestGitHubCommit(ctx, "oOflyeyesOo", "docktun", "main")
		if ghErr == nil && commitSHA != "" {
			res.LatestDigest = commitSHA
			if localCommit != "" {
				res.CurrentDigest = localCommit
				if !strings.HasPrefix(commitSHA, localCommit) && !strings.HasPrefix(localCommit, commitSHA) {
					res.UpdateAvailable = true
					res.Error = fmt.Sprintf("Upstream commit %s available (%s)", commitSHA, commitMsg)
				} else {
					res.UpdateAvailable = false
					res.Error = ""
				}
			} else {
				res.CurrentDigest = "local-git"
				res.UpdateAvailable = true
				res.Error = fmt.Sprintf("Upstream commit %s available (%s). Built locally.", commitSHA, commitMsg)
			}
		} else {
			res.CurrentDigest = "local-git"
			res.LatestDigest = "git-main"
			res.Error = "DockTun is built locally. Update via host git pull or switch to ghcr.io/ooflyeyesoo/docktun:latest."
			res.UpdateAvailable = false
		}
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
		if s.isSelfContainer(detail.ID, cname) || isDocktunContainer(cname, detail.Image) {
			commitSHA, commitMsg, ghErr := fetchLatestGitHubCommit(ctx, "oOflyeyesOo", "docktun", "main")
			if ghErr == nil && commitSHA != "" {
				res.LatestDigest = commitSHA
				localCommit := getLocalGitCommit(s.cfg.SelfPath, detail)
				if localCommit != "" {
					res.CurrentDigest = localCommit
					if !strings.HasPrefix(commitSHA, localCommit) && !strings.HasPrefix(localCommit, commitSHA) {
						res.UpdateAvailable = true
						res.Error = fmt.Sprintf("Upstream commit %s available (%s)", commitSHA, commitMsg)
					}
				} else {
					res.CurrentDigest = "local-git"
					res.UpdateAvailable = true
					res.Error = fmt.Sprintf("Upstream commit %s available (%s)", commitSHA, commitMsg)
				}
				s.saveUpdateToDB(res)
				return res, nil
			}
		}

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
	if current, e := selfContainer(context.Background()); e == nil && (id == current.ID || id == strings.TrimPrefix(current.Name, "/") || (len(id) >= 12 && strings.HasPrefix(current.ID, id))) {
		state, err := s.selfUpdater.Start(context.Background())
		if err == nil && s.audit != nil {
			s.audit.Log(userID, "self_update", state.Message, ip)
		}
		return "self-update: " + state.Message, err
	}

	detail, err := s.Details(id)
	if err != nil {
		return "", fmt.Errorf("failed to get container details: %w", err)
	}

	cname := strings.TrimPrefix(detail.Name, "/")
	if s.isSelfContainer(detail.ID, cname) {
		return "", fmt.Errorf("cannot safely identify DockTun for self-update; use Settings and configure DOCKTUN_CONTAINER if using a custom hostname")
	}
	strictStorage := reviewedProject(s.db, detail.ProjectID)
	image := detail.Image
	if image == "" {
		return "", fmt.Errorf("container has no image specified")
	}

	var logOutput strings.Builder
	appendLog := func(msg string) {
		log.Printf("[docktun-update] %s", msg)
		logOutput.WriteString(msg + "\n")
	}

	CleanupZombieUpdaters()

	if isLocalDocktunImage(image) {
		appendLog(fmt.Sprintf("[NOTE] Local image %s detected for %s. Using official image ghcr.io/ooflyeyesoo/docktun:latest for update...", image, cname))
		image = "ghcr.io/ooflyeyesoo/docktun:latest"
	}

	// Step 1: Inspect Compose metadata to determine rebuild strategy
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

	// Use the same verified project source as stack actions. Never guess an
	// unrelated /opt/projects folder from a basename or discard override files.
	validComposeDir := ""
	composeSources := []string{}
	if detail.ProjectID > 0 && s.db != nil {
		planner := NewProjects(s.db, s.cfg.SelfPath)
		if directory, err := planner.resolveProjectDirectory(detail.ProjectPath, false); err == nil {
			files, sourceErr := storedComposeSources(s.db, detail.ProjectID)
			if sourceErr == nil && len(files) == 0 {
				files, sourceErr = relativeComposeSources(compDir, compFile)
			}
			root, openErr := directory.open(false)
			if sourceErr == nil && openErr == nil {
				if len(files) == 0 {
					for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
						if _, err := readProjectFile(root, name); err == nil {
							files = []string{name}
							break
						}
					}
				}
				for _, name := range files {
					if _, err := readProjectFile(root, name); err != nil {
						sourceErr = err
						break
					}
					composeSources = append(composeSources, filepath.Join(directory.Path, name))
				}
				if sourceErr == nil && len(composeSources) > 0 {
					validComposeDir = directory.Path
				}
			}
			if root != nil {
				root.Close()
			}
		}
	}

	isLocalBuild := !strings.Contains(image, "/")

	if !isLocalBuild {
		appendLog(fmt.Sprintf("[STEP 1/4] Pulling latest image: %s...", image))
		pullCtx, pullCancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer pullCancel()

		pullCmd := exec.CommandContext(pullCtx, "docker", "pull", image)
		pullOut, err := pullCmd.CombinedOutput()
		appendLog(strings.TrimSpace(string(pullOut)))
		if err != nil {
			if validComposeDir != "" || compProj != "" {
				appendLog(fmt.Sprintf("[NOTE] Image pull notice (%s). Proceeding to Compose rebuild...", strings.TrimSpace(string(pullOut))))
			} else {
				return logOutput.String(), fmt.Errorf("failed to pull image %s: %s (%w)", image, strings.TrimSpace(string(pullOut)), err)
			}
		}
	} else {
		appendLog(fmt.Sprintf("[STEP 1/4] Local image %s has no remote registry. Proceeding to Compose rebuild...", image))
	}

	recreated := false

	// Strategy A: Existing valid compose stack
	if validComposeDir != "" {

		if isLocalBuild {
			appendLog(fmt.Sprintf("[BUILD] Building local service %s via Docker Compose in %s...", compService, validComposeDir))
			buildCmd, buildPlanErr := composeProjectCommandPolicy(compProj, validComposeDir, composeSources, strictStorage, "build", compService)
			var bOut []byte
			bErr := buildPlanErr
			if bErr == nil {
				bOut, bErr = buildCmd.CombinedOutput()
			}

			appendLog(strings.TrimSpace(string(bOut)))
			if bErr != nil {
				appendLog(fmt.Sprintf("[WARN] Compose build notice: %v", bErr))
			}
		}

		appendLog(fmt.Sprintf("[STEP 2/4] Updating via Docker Compose in %s (service: %s)...", validComposeDir, compService))
		upCmd, planErr := composeProjectCommandPolicy(compProj, validComposeDir, composeSources, strictStorage, "up", "-d", "--no-deps", "--force-recreate", compService)
		if planErr != nil {
			return logOutput.String(), fmt.Errorf("validate project before update: %w", planErr)
		}
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
					retryCmd, retryPlanErr := composeProjectCommandPolicy(compProj, validComposeDir, composeSources, strictStorage, "up", "-d", "--no-deps", "--force-recreate", compService)
					var retryOut []byte
					retryErr := retryPlanErr
					if retryErr == nil {
						retryOut, retryErr = retryCmd.CombinedOutput()
					}

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

	if !recreated && strictStorage {
		return logOutput.String(), fmt.Errorf("reviewed stack could not be recreated from its Compose sources; restore its files and host storage before retrying")
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
			backupName := fmt.Sprintf("%s_bk_%d", cname, time.Now().Unix())
			_ = exec.Command("docker", "stop", detail.ID).Run()
			if renameErr := exec.Command("docker", "rename", detail.ID, backupName).Run(); renameErr == nil {
				upCmd, upPlanErr := composeProjectCommandWithFile(cname, destDir, composeFilePath, "up", "-d", "--no-deps", "--force-recreate")
				var upOut []byte
				upErr := upPlanErr
				if upErr == nil {
					upOut, upErr = upCmd.CombinedOutput()
				}

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
	isHostNet := detail.NetworkMode == "host"
	if !isHostNet {
		for _, net := range detail.Networks {
			if net == "host" {
				isHostNet = true
				break
			}
		}
	}
	if !isHostNet && (detail.ExposureMode == "public" || detail.ExposureMode == "private") {
		netName := s.DefaultNetwork()
		appendLog(fmt.Sprintf("[NETWORK] Ensuring connection to %s network...", netName))
		_ = s.ConnectNetwork(cname, netName)
	}

	// Auto-heal companion database and service links (e.g. mediastorm -> mediastorm-postgres with alias postgres)
	if linkErr := s.EnsureDatabaseLinks(cname); linkErr != nil {
		appendLog(fmt.Sprintf("[NOTE] Database link verification notice: %v", linkErr))
	} else {
		appendLog(fmt.Sprintf("[DATABASE] Configured dependency networks checked for %s.", cname))
	}

	targetID := id
	if fresh, err := s.Details(cname); err == nil && fresh.ID != "" {
		targetID = fresh.ID
	}
	s.setContainerUpToDate(targetID, cname, image)

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

	isHostNet := detail.NetworkMode == "host"
	if !isHostNet {
		for _, net := range detail.Networks {
			if net == "host" {
				isHostNet = true
				break
			}
		}
	}

	var runArgs []string
	runArgs = append(runArgs, "create", "--name", cname, "--restart", restartPol)
	if isHostNet {
		runArgs = append(runArgs, "--network", "host")
	} else if primaryNet != "" {
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

	if !isHostNet && detail.Ports != "" {
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
		return fmt.Errorf("use the dedicated DockTun self-updater")
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
	if !isHostNet {
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

	targetID := id
	if fresh, err := s.Details(name); err == nil && fresh.ID != "" {
		targetID = fresh.ID
	}

	_, _ = s.db.Exec(`DELETE FROM container_updates WHERE container_id = ? OR container_id = ? OR container_name = ?`, id, targetID, name)

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
	`, targetID, name, image, newDigest, newDigest)
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

func isAuxiliaryDocktunService(name, image string) bool {
	n := strings.ToLower(strings.TrimPrefix(name, "/"))
	img := strings.ToLower(strings.TrimSpace(image))
	return strings.Contains(n, "zeroconf") || strings.Contains(img, "zeroconf") ||
		strings.Contains(n, "cloudflared") || strings.Contains(img, "cloudflared") ||
		strings.Contains(n, "wireguard") || strings.Contains(img, "wireguard") ||
		strings.Contains(n, "tailscale") || strings.Contains(img, "tailscale")
}

func isDocktunContainer(name, image string) bool {
	if isAuxiliaryDocktunService(name, image) {
		return false
	}
	n := strings.ToLower(strings.TrimPrefix(name, "/"))
	img := strings.ToLower(strings.TrimSpace(image))
	return n == "docktun" || n == "docktun-backend" || n == "docktun-frontend" ||
		strings.HasPrefix(n, "docktun-docktun") || strings.HasPrefix(n, "tun-docktun") ||
		img == "docktun" || img == "docktun:latest" ||
		strings.HasSuffix(img, "/docktun:latest") || strings.HasSuffix(img, "/docktun") ||
		strings.HasSuffix(img, "-docktun:latest") || strings.HasSuffix(img, "_docktun:latest")
}

func isLocalDocktunImage(image string) bool {
	if strings.Contains(image, "/") {
		return false
	}
	if isAuxiliaryDocktunService("", image) {
		return false
	}
	img := strings.ToLower(strings.TrimSpace(image))
	return img == "docktun" || img == "docktun:latest" || strings.HasSuffix(img, "-docktun:latest") || strings.HasSuffix(img, "_docktun:latest")
}

func getLocalGitCommit(selfPath string, detail ContainerDetail) string {
	if rev, ok := detail.Labels["org.opencontainers.image.revision"]; ok && rev != "" {
		if len(rev) > 7 {
			return rev[:7]
		}
		return rev
	}
	if rev, ok := detail.Labels["git_commit"]; ok && rev != "" {
		if len(rev) > 7 {
			return rev[:7]
		}
		return rev
	}
	if c := os.Getenv("GIT_COMMIT"); c != "" {
		if len(c) > 7 {
			return c[:7]
		}
		return c
	}
	paths := []string{selfPath, "/opt/projects/docktun", "/app", "."}
	for _, p := range paths {
		if p == "" {
			continue
		}
		gitDir := filepath.Join(p, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			cmd := exec.Command("git", "-C", p, "rev-parse", "--short", "HEAD")
			if out, err := cmd.Output(); err == nil {
				commit := strings.TrimSpace(string(out))
				if commit != "" {
					return commit
				}
			}
		}
	}
	return ""
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

var reIPv4 = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
var reBracketedIP = regexp.MustCompile(`\[[^\]]+\]`)

func cleanPortNumber(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'[] `)
	s = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(s), "/tcp"), "/udp")
	s = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(s), "tcp/"), "udp/")
	s = strings.TrimSpace(s)
	// If it contains a dot, it is an IP address or host, NOT a port number!
	if strings.Contains(s, ".") {
		return ""
	}
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
	// Strip IPv4 addresses and bracketed IPv6 first so IP octets are NEVER parsed as ports
	s = reIPv4.ReplaceAllString(s, "")
	s = reBracketedIP.ReplaceAllString(s, "")

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
	rawLower := strings.ToLower(raw)
	if strings.Contains(rawLower, "/udp") {
		proto = "udp"
	}

	// Clean out protocol markers from raw string
	cleanRaw := raw
	cleanRaw = strings.ReplaceAll(cleanRaw, "/tcp", "")
	cleanRaw = strings.ReplaceAll(cleanRaw, "/udp", "")
	cleanRaw = strings.ReplaceAll(cleanRaw, "/TCP", "")
	cleanRaw = strings.ReplaceAll(cleanRaw, "/UDP", "")
	cleanRaw = strings.TrimSpace(cleanRaw)

	// Case 1: contains "->" (Docker ps / inspect format, e.g. "0.0.0.0:80->80/tcp", "127.0.0.1:8191->8191/tcp", "8080->80")
	if strings.Contains(cleanRaw, "->") {
		parts := strings.SplitN(cleanRaw, "->", 2)
		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])

		var hostSide, containerSide string
		if strings.Contains(left, ".") || strings.Contains(left, ":") || !strings.Contains(right, ".") {
			hostSide = left
			containerSide = right
		} else {
			hostSide = right
			containerSide = left
		}

		cPort := cleanPortNumber(containerSide)
		hostIP := ""
		hostPort := ""

		if strings.HasPrefix(hostSide, "[") && strings.Contains(hostSide, "]") {
			bracketEnd := strings.Index(hostSide, "]")
			hostIP = hostSide[1:bracketEnd]
			hostPort = cleanPortNumber(hostSide[bracketEnd+1:])
		} else if strings.Contains(hostSide, ".") {
			if lastColon := strings.LastIndex(hostSide, ":"); lastColon >= 0 {
				hostIP = strings.TrimSpace(hostSide[:lastColon])
				hostPort = cleanPortNumber(hostSide[lastColon+1:])
			} else {
				hostPort = cleanPortNumber(hostSide)
			}
		} else if strings.Contains(hostSide, ":::") {
			hostPort = cleanPortNumber(hostSide)
		} else if lastColon := strings.LastIndex(hostSide, ":"); lastColon >= 0 {
			hostIP = strings.TrimSpace(hostSide[:lastColon])
			hostPort = cleanPortNumber(hostSide[lastColon+1:])
		} else {
			hostPort = cleanPortNumber(hostSide)
		}
		hostIP = strings.Trim(hostIP, "[]")
		if hostIP == "0.0.0.0" || hostIP == "::" || hostIP == ":::" || hostIP == "[::]" {
			hostIP = ""
		}

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

	// Case 2: Bracketed IPv6, e.g. "[::1]:8080:80" or "[::]:80"
	if strings.HasPrefix(cleanRaw, "[") && strings.Contains(cleanRaw, "]") {
		bracketEnd := strings.Index(cleanRaw, "]")
		ip := cleanRaw[1:bracketEnd]
		rest := strings.TrimPrefix(cleanRaw[bracketEnd+1:], ":")
		parts := strings.Split(rest, ":")
		if len(parts) == 2 {
			h := cleanPortNumber(parts[0])
			c := cleanPortNumber(parts[1])
			if h != "" && c != "" {
				return ParsedPortMapping{
					HostIP:        ip,
					HostPort:      h,
					ContainerPort: c,
					Protocol:      proto,
				}, true
			}
		} else if len(parts) == 1 {
			p := cleanPortNumber(parts[0])
			if p != "" {
				return ParsedPortMapping{
					HostIP:        ip,
					HostPort:      p,
					ContainerPort: p,
					Protocol:      proto,
				}, true
			}
		}
	}

	// Case 3: Multiple colons with ::: (e.g. ":::8191:8191" or ":::8191")
	if strings.Contains(cleanRaw, ":::") {
		nums := extractAllPortNumbers(cleanRaw)
		if len(nums) >= 2 {
			return ParsedPortMapping{
				HostPort:      nums[len(nums)-2],
				ContainerPort: nums[len(nums)-1],
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

	// Case 4: Standard colon-separated
	if strings.Contains(cleanRaw, ":") {
		colons := strings.Split(cleanRaw, ":")
		if len(colons) == 2 {
			part1 := strings.TrimSpace(colons[0])
			part2 := strings.TrimSpace(colons[1])

			// Check if part1 is an IP address (e.g. "127.0.0.1:8191" or "0.0.0.0:80")
			if strings.Contains(part1, ".") || net.ParseIP(part1) != nil {
				c := cleanPortNumber(part2)
				if c != "" {
					ip := part1
					if ip == "0.0.0.0" || ip == "::" || ip == ":::" || ip == "[::]" {
						ip = ""
					}
					return ParsedPortMapping{
						HostIP:        ip,
						HostPort:      c,
						ContainerPort: c,
						Protocol:      proto,
					}, true
				}
			}

			// Normal "host:container" (e.g. "8080:80")
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

			ip := strings.Trim(part1, "[]")
			if ip == "0.0.0.0" || ip == "::" || ip == ":::" || ip == "[::]" {
				ip = ""
			}
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
			// len(colons) > 3, e.g. unbracketed IPv6 or corrupted lines
			nums := extractAllPortNumbers(cleanRaw)
			if len(nums) >= 2 {
				return ParsedPortMapping{
					HostPort:      nums[len(nums)-2],
					ContainerPort: nums[len(nums)-1],
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

	// Case 5: Single port number (e.g. "80" or "8080")
	clean := cleanPortNumber(cleanRaw)
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

				// Check for known corrupted 127 port artifacts:
				// Pattern 1: "127:1" or "127:1/tcp" (corrupted from 127.0.0.1 octets)
				if rawPort == "127:1" || rawPort == "127:1/tcp" || strings.HasPrefix(rawPort, "127:1:") {
					dashIdx := strings.Index(line, "-")
					indent := line[:dashIdx]
					lines[i] = fmt.Sprintf("%s# removed corrupted 127.0.0.1 artifact (%s)", indent, rawPort)
					modified = true
					continue
				}

				// Pattern 2: "127:<PORT>" where PORT > 100 (e.g. "127:8191") -> heal to "127.0.0.1:<PORT>:<PORT>"
				if strings.HasPrefix(rawPort, "127:") && !strings.Contains(rawPort, ".") {
					portAfter := strings.TrimPrefix(rawPort, "127:")
					portAfterClean := cleanPortNumber(portAfter)
					if pNum, err := strconv.Atoi(portAfterClean); err == nil && pNum > 100 {
						proto := ""
						if strings.Contains(strings.ToLower(rawPort), "udp") {
							proto = "/udp"
						}
						healed := fmt.Sprintf("127.0.0.1:%d:%d%s", pNum, pNum, proto)
						dashIdx := strings.Index(line, "-")
						indent := line[:dashIdx]
						lines[i] = fmt.Sprintf("%s- \"%s\"", indent, healed)
						modified = true
						seenPorts[healed] = true
						continue
					}
				}

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
	}

	volModified := SanitizeComposeVolumes(composePath)
	return modified || volModified
}

func sanitizeComposeFile(composePath string) bool {
	return SanitizeComposeFile(composePath)
}
