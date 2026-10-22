package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"docktun/internal/config"
)

type Metrics struct {
	cfg *config.Config
}

func NewMetrics(cfg *config.Config) *Metrics {
	return &Metrics{cfg: cfg}
}

type metricsResponse struct {
	TotalRequests  int64            `json:"total_requests"`
	ActiveConns    int64            `json:"active_connections"`
	HAConnections  int64            `json:"ha_connections"`
	ColoLocations  []string         `json:"colo_locations"`
	ResponseCodes  map[string]int64 `json:"response_codes"`
	Raw            string           `json:"raw,omitempty"`
}

func (h *Metrics) Get(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get(h.cfg.MetricsAddr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metricsResponse{ResponseCodes: make(map[string]int64), ColoLocations: []string{}})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	raw := string(body)

	m := metricsResponse{
		ResponseCodes: make(map[string]int64),
		ColoLocations: []string{},
	}
	m.Raw = raw

	re := regexp.MustCompile(`cloudflared_tunnel_total_requests\s+(\d+)`)
	if matches := re.FindStringSubmatch(raw); len(matches) > 1 {
		m.TotalRequests, _ = strconv.ParseInt(matches[1], 10, 64)
	}

	reConn := regexp.MustCompile(`cloudflared_tunnel_active_streams\s+(\d+)`)
	if matches := reConn.FindStringSubmatch(raw); len(matches) > 1 {
		m.ActiveConns, _ = strconv.ParseInt(matches[1], 10, 64)
	}

	reHA := regexp.MustCompile(`cloudflared_tunnel_ha_connections\s+(\d+)`)
	if matches := reHA.FindStringSubmatch(raw); len(matches) > 1 {
		m.HAConnections, _ = strconv.ParseInt(matches[1], 10, 64)
	}

	reColo := regexp.MustCompile(`cloudflared_tunnel_server_locations\{[^}]*location="([A-Za-z0-9_-]+)"[^}]*\}\s+1`)
	coloSet := make(map[string]bool)
	for _, match := range reColo.FindAllStringSubmatch(raw, -1) {
		if len(match) > 1 {
			coloSet[match[1]] = true
		}
	}
	for c := range coloSet {
		m.ColoLocations = append(m.ColoLocations, c)
	}

	reStatus := regexp.MustCompile(`cloudflared_tunnel_response_by_code\{status_code="(\d+)"\}\s+(\d+)`)
	for _, line := range strings.Split(raw, "\n") {
		if matches := reStatus.FindStringSubmatch(line); len(matches) > 2 {
			count, _ := strconv.ParseInt(matches[2], 10, 64)
			m.ResponseCodes[matches[1]] = count
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}
