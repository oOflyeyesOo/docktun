package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

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
	Metrics        string   `json:"metrics,omitempty"`
	ZeroconfStatus string   `json:"zeroconf_status,omitempty"`
}

func (h *Status) Get(w http.ResponseWriter, r *http.Request) {
	state := h.cfd.Status()

	hostnames := h.extractHostnames()
	resp := statusResponse{ProcessState: state}
	resp.TunnelName = h.cfg.TunnelName
	resp.Hostnames = hostnames
	resp.IngressCount = len(hostnames)

	if state.Running {
		if metricsResp, err := http.Get(h.cfg.MetricsAddr); err == nil {
			defer metricsResp.Body.Close()
			body, _ := io.ReadAll(metricsResp.Body)
			resp.Metrics = string(body)
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
