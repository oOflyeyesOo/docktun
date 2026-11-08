package handler

import (
	"context"
	"docktun/internal/service"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type HealthCheck struct {
	configPath   string
	activeConfig func() (string, error)
	mu           sync.RWMutex
	cacheTime    time.Time
	cacheData    []serviceHealth
}

func NewHealthCheck(configPath string) *HealthCheck {
	return &HealthCheck{configPath: configPath}
}

func (h *HealthCheck) WithActiveConfig(api *service.CloudflareAPI, mgr *service.ConfigManager) *HealthCheck {
	h.activeConfig = func() (string, error) { return api.ActiveConfig(mgr) }
	return h
}

type serviceHealth struct {
	Hostname  string `json:"hostname"`
	Service   string `json:"service"`
	Status    string `json:"status"`
	Latency   string `json:"latency"`
	LatencyMs int64  `json:"latency_ms"`
}

func (h *HealthCheck) Check(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"
	h.mu.RLock()
	if !force && time.Since(h.cacheTime) < 12*time.Second && len(h.cacheData) > 0 {
		cached := h.cacheData
		h.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached)
		return
	}
	h.mu.RUnlock()
	var cfg struct {
		Ingress []struct {
			Hostname string `yaml:"hostname"`
			Service  string `yaml:"service"`
		} `yaml:"ingress"`
	}

	data, err := os.ReadFile(h.configPath)
	if h.activeConfig != nil {
		var content string
		content, err = h.activeConfig()
		data = []byte(content)
	}
	if err != nil {
		jsonError(w, "cannot read active tunnel configuration; check Cloudflare API credentials and connectivity", http.StatusInternalServerError)
		return
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		jsonError(w, "invalid tunnel configuration", http.StatusInternalServerError)
		return
	}

	type ruleTarget struct {
		Hostname string
		Service  string
	}
	var targets []ruleTarget
	for _, ing := range cfg.Ingress {
		hname := strings.TrimSpace(ing.Hostname)
		svc := strings.TrimSpace(ing.Service)
		if hname == "" || svc == "http_status:404" {
			continue
		}
		targets = append(targets, ruleTarget{Hostname: hname, Service: svc})
	}

	results := make([]serviceHealth, len(targets))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, target ruleTarget) {
			defer wg.Done()
			s := serviceHealth{Hostname: target.Hostname, Service: target.Service}
			start := time.Now()

			host, err := healthAddress(target.Service)
			if err != nil {
				s.Status = "invalid"
				s.Latency = "invalid target"
				s.LatencyMs = -1
				results[idx] = s
				return
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-r.Context().Done():
				s.Status = "down"
				s.Latency = "cancelled"
				s.LatencyMs = -1
				results[idx] = s
				return
			}
			start = time.Now()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", host)
			dur := time.Since(start)
			if err != nil {
				s.Status = "down"
				s.Latency = "unreachable"
				s.LatencyMs = -1
			} else {
				conn.Close()
				s.Status = "up"
				s.Latency = dur.Truncate(time.Millisecond).String()
				s.LatencyMs = dur.Milliseconds()
			}
			results[idx] = s
		}(i, t)
	}
	wg.Wait()
	h.mu.Lock()
	h.cacheData = results
	h.cacheTime = time.Now()
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// healthAddress validates TCP-based ingress targets without guessing ports for
// arbitrary schemes. JoinHostPort preserves IPv6 literals.
func healthAddress(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("invalid target")
	}
	port := u.Port()
	switch u.Scheme {
	case "http", "https":
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
	case "tcp", "ssh", "rdp":
		if port == "" {
			return "", fmt.Errorf("explicit port required")
		}
	default:
		return "", fmt.Errorf("unsupported scheme")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("invalid port")
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}
