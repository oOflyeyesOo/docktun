package handler

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type HealthCheck struct {
	configPath string
}

func NewHealthCheck(configPath string) *HealthCheck {
	return &HealthCheck{configPath: configPath}
}

type serviceHealth struct {
	Hostname  string `json:"hostname"`
	Service   string `json:"service"`
	Status    string `json:"status"`
	Latency   string `json:"latency"`
	LatencyMs int64  `json:"latency_ms"`
}

func (h *HealthCheck) Check(w http.ResponseWriter, r *http.Request) {
	var cfg struct {
		Ingress []struct {
			Hostname string `yaml:"hostname"`
			Service  string `yaml:"service"`
		} `yaml:"ingress"`
	}

	data, err := os.ReadFile(h.configPath)
	if err != nil {
		http.Error(w, `{"error":"cannot read config"}`, http.StatusInternalServerError)
		return
	}
	_ = yaml.Unmarshal(data, &cfg)

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
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, target ruleTarget) {
			defer wg.Done()
			s := serviceHealth{Hostname: target.Hostname, Service: target.Service}
			start := time.Now()

			u, err := url.Parse(target.Service)
			if err != nil {
				s.Status = "invalid"
				results[idx] = s
				return
			}

			host := u.Host
			if !strings.Contains(host, ":") {
				if u.Scheme == "https" {
					host += ":443"
				} else {
					host += ":80"
				}
			}

			conn, err := net.DialTimeout("tcp", host, 3*time.Second)
			dur := time.Since(start)
			if err != nil {
				s.Status = "down"
				s.Latency = "timeout"
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
