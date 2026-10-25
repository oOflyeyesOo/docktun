package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"docktun/internal/config"
	"docktun/internal/service"
	"gopkg.in/yaml.v3"
)

type DockerSystemStats struct {
	ServerVersion     string `json:"server_version,omitempty"`
	OperatingSystem   string `json:"operating_system,omitempty"`
	Architecture      string `json:"architecture,omitempty"`
	CPUs              int    `json:"cpus,omitempty"`
	MemTotal          int64  `json:"mem_total,omitempty"`
	MemTotalStr       string `json:"mem_total_str,omitempty"`
	ImagesCount       int    `json:"images_count,omitempty"`
	ContainersTotal   int    `json:"containers_total,omitempty"`
	ContainersRunning int    `json:"containers_running,omitempty"`
	ContainersPaused  int    `json:"containers_paused,omitempty"`
	ContainersStopped int    `json:"containers_stopped,omitempty"`
}

type Status struct {
	cfd      *service.Cloudflared
	cfg      *config.Config
	sysMu    sync.RWMutex
	sysCache DockerSystemStats
	sysTime  time.Time
}

func NewStatus(cfd *service.Cloudflared, cfg *config.Config) *Status {
	return &Status{cfd: cfd, cfg: cfg}
}

type statusResponse struct {
	service.ProcessState
	TunnelName     string            `json:"tunnel_name"`
	Hostnames      []string          `json:"hostnames"`
	IngressCount   int               `json:"ingress_count"`
	ColoLocations  []string          `json:"colo_locations"`
	HAConnections  int64             `json:"ha_connections"`
	TotalRequests  int64             `json:"total_requests"`
	ActiveConns    int64             `json:"active_connections"`
	Metrics        string            `json:"metrics,omitempty"`
	ZeroconfStatus string            `json:"zeroconf_status,omitempty"`
	DockerSystem   DockerSystemStats `json:"docker_system,omitempty"`
}

func (h *Status) Get(w http.ResponseWriter, r *http.Request) {
	state := h.cfd.Status()

	hostnames := h.extractHostnames()
	resp := statusResponse{
		ProcessState:  state,
		TunnelName:    h.cfg.TunnelName,
		Hostnames:     hostnames,
		IngressCount:  len(hostnames),
		ColoLocations: []string{},
	}

	if state.Running {
		metricsClient := &http.Client{Timeout: 1500 * time.Millisecond}
		if metricsResp, err := metricsClient.Get(h.cfg.MetricsAddr); err == nil {
			defer metricsResp.Body.Close()
			body, _ := io.ReadAll(metricsResp.Body)
			raw := string(body)
			resp.Metrics = raw

			re := regexp.MustCompile(`cloudflared_tunnel_total_requests\s+(\d+)`)
			if matches := re.FindStringSubmatch(raw); len(matches) > 1 {
				resp.TotalRequests, _ = strconv.ParseInt(matches[1], 10, 64)
			}
			reConn := regexp.MustCompile(`cloudflared_tunnel_active_streams\s+(\d+)`)
			if matches := reConn.FindStringSubmatch(raw); len(matches) > 1 {
				resp.ActiveConns, _ = strconv.ParseInt(matches[1], 10, 64)
			}
			reHA := regexp.MustCompile(`cloudflared_tunnel_ha_connections\s+(\d+)`)
			if matches := reHA.FindStringSubmatch(raw); len(matches) > 1 {
				resp.HAConnections, _ = strconv.ParseInt(matches[1], 10, 64)
			}
			reColo := regexp.MustCompile(`cloudflared_tunnel_server_locations\{[^}]*location="([A-Za-z0-9_-]+)"[^}]*\}\s+1`)
			coloSet := make(map[string]bool)
			for _, match := range reColo.FindAllStringSubmatch(raw, -1) {
				if len(match) > 1 {
					coloSet[match[1]] = true
				}
			}
			for c := range coloSet {
				resp.ColoLocations = append(resp.ColoLocations, c)
			}
		}
	}

	// Check Zeroconf proxy container status
	ctxZC, cancelZC := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancelZC()
	if zcOut, err := exec.CommandContext(ctxZC, "docker", "inspect", "-f", "{{.State.Status}}", "docktun-zeroconf").Output(); err == nil {
		resp.ZeroconfStatus = strings.TrimSpace(string(zcOut))
	} else {
		resp.ZeroconfStatus = "not found"
	}

	resp.DockerSystem = h.getDockerSystemStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Status) getDockerSystemStats() DockerSystemStats {
	h.sysMu.RLock()
	if time.Since(h.sysTime) < 15*time.Second && h.sysCache.ServerVersion != "" {
		cached := h.sysCache
		h.sysMu.RUnlock()
		return cached
	}
	h.sysMu.RUnlock()

	h.sysMu.Lock()
	defer h.sysMu.Unlock()
	if time.Since(h.sysTime) < 15*time.Second && h.sysCache.ServerVersion != "" {
		return h.sysCache
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .}}")
	out, err := cmd.Output()
	if err != nil {
		return h.sysCache
	}

	var raw struct {
		ServerVersion     string `json:"ServerVersion"`
		OperatingSystem   string `json:"OperatingSystem"`
		Architecture      string `json:"Architecture"`
		NCPU              int    `json:"NCPU"`
		MemTotal          int64  `json:"MemTotal"`
		Images            int    `json:"Images"`
		Containers        int    `json:"Containers"`
		ContainersRunning int    `json:"ContainersRunning"`
		ContainersPaused  int    `json:"ContainersPaused"`
		ContainersStopped int    `json:"ContainersStopped"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return h.sysCache
	}

	stats := DockerSystemStats{
		ServerVersion:     raw.ServerVersion,
		OperatingSystem:   raw.OperatingSystem,
		Architecture:      raw.Architecture,
		CPUs:              raw.NCPU,
		MemTotal:          raw.MemTotal,
		ImagesCount:       raw.Images,
		ContainersTotal:   raw.Containers,
		ContainersRunning: raw.ContainersRunning,
		ContainersPaused:  raw.ContainersPaused,
		ContainersStopped: raw.ContainersStopped,
	}
	if raw.MemTotal > 0 {
		gb := float64(raw.MemTotal) / (1024 * 1024 * 1024)
		stats.MemTotalStr = fmt.Sprintf("%.1f GB", gb)
	}

	h.sysCache = stats
	h.sysTime = time.Now()
	return stats
}

func (h *Status) extractHostnames() []string {
	var cfg struct {
		Ingress []struct {
			Hostname string `yaml:"hostname"`
		} `yaml:"ingress"`
	}

	data, err := os.ReadFile(h.cfg.CloudflaredConfig)
	if err != nil {
		return nil
	}
	yaml.Unmarshal(data, &cfg)

	var hosts []string
	for _, ing := range cfg.Ingress {
		if ing.Hostname != "" {
			hosts = append(hosts, ing.Hostname)
		}
	}
	return hosts
}
