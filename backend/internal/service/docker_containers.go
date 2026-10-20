package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luzyver/docktun/internal/config"
	"github.com/luzyver/docktun/internal/db"
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
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Image        string           `json:"image"`
	Status       string           `json:"status"`
	State        string           `json:"state"`
	Ports        string           `json:"ports"`
	Command      string           `json:"command"`
	Created      string           `json:"created"`
	Running      bool             `json:"running"`
	ExposureMode string           `json:"exposure_mode"`
	Hostname     string           `json:"hostname,omitempty"`
	Target       string           `json:"target,omitempty"`
	PrivateType  string           `json:"private_type,omitempty"`
	PrivateHost  string           `json:"private_host,omitempty"`
	PrivatePort  int              `json:"private_port,omitempty"`
	Notes        string           `json:"notes,omitempty"`
	CPU          string           `json:"cpu,omitempty"`
	Memory       string           `json:"memory,omitempty"`
	Network      string           `json:"network,omitempty"`
	BlockIO      string           `json:"block_io,omitempty"`
	PIDs         int              `json:"pids,omitempty"`
	RestartCount int              `json:"restart_count,omitempty"`
	Uptime       string           `json:"uptime,omitempty"`
	Exposure     ExposureSettings `json:"exposure,omitempty"`
}

type ContainerDetail struct {
	Container
	Env        []string         `json:"env,omitempty"`
	Mounts     []ContainerMount `json:"mounts,omitempty"`
	IPAddress  string           `json:"ip_address,omitempty"`
	Gateway    string           `json:"gateway,omitempty"`
	MacAddress string           `json:"mac_address,omitempty"`
	Networks   []string         `json:"networks,omitempty"`
	ExitCode   int              `json:"exit_code,omitempty"`
}

type cfIngressRule struct {
	Hostname string `yaml:"hostname,omitempty"`
	Service  string `yaml:"service"`
	Path     string `yaml:"path,omitempty"`
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
}

func NewDockerContainers(cfg *config.Config, db *db.DB, configMgr *ConfigManager, cloudflared *Cloudflared) *DockerContainers {
	return &DockerContainers{
		cfg:         cfg,
		db:          db,
		configMgr:   configMgr,
		cloudflared: cloudflared,
	}
}

type liveStats struct {
	cpu     string
	mem     string
	net     string
	block   string
	pids    int
}

func (s *DockerContainers) List() ([]Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.State}}\t{{.Ports}}\t{{.RunningFor}}\t{{.Command}}")
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

	rows := strings.Split(text, "\n")
	containers := make([]Container, 0, len(rows))

	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}

		fields := strings.SplitN(row, "\t", 8)
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

	return detail, nil
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

func (s *DockerContainers) Logs(id string, tail int) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", strconv.Itoa(tail), id)
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

func (s *DockerContainers) UpdateExposure(id string, settings ExposureSettings) error {
	name, err := s.containerName(id)
	if err != nil {
		return err
	}

	if settings.Mode == "" {
		settings.Mode = "internal"
	}

	// 1. Update Cloudflare ingress if mode is public or if changing away from public
	if s.configMgr != nil {
		if err := s.syncCloudflareIngress(name, settings); err != nil {
			return fmt.Errorf("sync ingress config: %w", err)
		}
	}

	// 2. Persist in database
	return s.saveExposureToDB(name, settings)
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

	// Filter out any existing rule for this container
	newIngress := make([]cfIngressRule, 0, len(doc.Ingress)+1)
	for _, rule := range doc.Ingress {
		// Ignore catch-all rule during filter
		if rule.Service == "http_status:404" {
			continue
		}
		// If rule matches container name or hostname
		if strings.Contains(rule.Service, containerName) || (settings.Hostname != "" && rule.Hostname == settings.Hostname) {
			continue
		}
		newIngress = append(newIngress, rule)
	}

	// If public mode, add new rule
	if settings.Mode == "public" && settings.Hostname != "" {
		target := settings.Target
		if target == "" {
			target = fmt.Sprintf("http://%s:80", containerName)
		}
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "tcp://") {
			target = "http://" + target
		}
		newIngress = append(newIngress, cfIngressRule{
			Hostname: settings.Hostname,
			Service:  target,
		})
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

	// Reload cloudflared if running
	if s.cloudflared != nil {
		go func() {
			_ = s.cloudflared.Restart()
		}()
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
