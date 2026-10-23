package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"docktun/internal/config"
	"docktun/internal/service"
	"gopkg.in/yaml.v3"
)

type Status struct {
	cfd *service.Cloudflared
	cfg *config.Config
}

func NewStatus(cfd *service.Cloudflared, cfg *config.Config) *Status {
	return &Status{cfd: cfd, cfg: cfg}
}

type statusResponse struct {
	service.ProcessState
	TunnelName     string   `json:"tunnel_name"`
	Hostnames      []string `json:"hostnames"`
	IngressCount   int      `json:"ingress_count"`
	ColoLocations  []string `json:"colo_locations"`
	HAConnections  int64    `json:"ha_connections"`
	TotalRequests  int64    `json:"total_requests"`
	ActiveConns    int64    `json:"active_connections"`
	Metrics        string   `json:"metrics,omitempty"`
	ZeroconfStatus string   `json:"zeroconf_status,omitempty"`
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
	if zcOut, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", "docktun-zeroconf").Output(); err == nil {
		resp.ZeroconfStatus = strings.TrimSpace(string(zcOut))
	} else {
		resp.ZeroconfStatus = "not found"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
