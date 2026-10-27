package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"docktun/internal/config"
	"docktun/internal/service"
)

type Metrics struct {
	cfg        *config.Config
	containers *service.DockerContainers
}

func NewMetrics(cfg *config.Config, containers *service.DockerContainers) *Metrics {
	return &Metrics{
		cfg:        cfg,
		containers: containers,
	}
}

type HostMetrics struct {
	CPUs              int        `json:"cpus"`
	LoadAvg           [3]float64 `json:"load_avg"`
	TotalCPUPercent   float64    `json:"total_cpu_percent"`
	MemTotal          int64      `json:"mem_total"`
	MemUsed           int64      `json:"mem_used"`
	MemFree           int64      `json:"mem_free"`
	MemPercent        float64    `json:"mem_percent"`
	DiskTotal         int64      `json:"disk_total"`
	DiskUsed          int64      `json:"disk_used"`
	DiskFree          int64      `json:"disk_free"`
	DiskPercent       float64    `json:"disk_percent"`
	UptimeSeconds     int64      `json:"uptime_seconds"`
	ContainersTotal   int        `json:"containers_total"`
	ContainersRunning int        `json:"containers_running"`
	ContainersStopped int        `json:"containers_stopped"`
}

type ContainerMetric struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Image         string  `json:"image"`
	State         string  `json:"state"`
	Status        string  `json:"status"`
	Project       string  `json:"project,omitempty"`
	CPUPercent    string  `json:"cpu_percent"`
	CPUNum        float64 `json:"cpu_num"`
	MemUsage      string  `json:"mem_usage"`
	MemLimit      string  `json:"mem_limit"`
	MemBytes      int64   `json:"mem_bytes"`
	MemLimitBytes int64   `json:"mem_limit_bytes"`
	MemPercent    float64 `json:"mem_percent"`
	NetIO         string  `json:"net_io"`
	BlockIO       string  `json:"block_io"`
	PIDs          int     `json:"pids"`
}

type metricsResponse struct {
	// Host & System
	Host HostMetrics `json:"host"`

	// Containers
	Containers []ContainerMetric `json:"containers"`

	// Cloudflare Tunnel metrics
	TotalRequests int64            `json:"total_requests"`
	ActiveConns   int64            `json:"active_connections"`
	HAConnections int64            `json:"ha_connections"`
	ColoLocations []string         `json:"colo_locations"`
	ResponseCodes map[string]int64 `json:"response_codes"`
	Raw           string           `json:"raw,omitempty"`
}

func (h *Metrics) Get(w http.ResponseWriter, r *http.Request) {
	resp := metricsResponse{
		ResponseCodes: make(map[string]int64),
		ColoLocations: []string{},
		Containers:    make([]ContainerMetric, 0),
		Host: HostMetrics{
			CPUs:    runtime.NumCPU(),
			LoadAvg: readLoadAvg(),
		},
	}

	// Read host uptime
	resp.Host.UptimeSeconds = readUptime()

	// Read host memory from /proc/meminfo
	resp.Host.MemTotal, resp.Host.MemUsed, resp.Host.MemFree, resp.Host.MemPercent = readHostMemory()

	// Read host disk space
	resp.Host.DiskTotal, resp.Host.DiskUsed, resp.Host.DiskFree, resp.Host.DiskPercent = readDiskSpace()

	// Gather container metrics
	if h.containers != nil {
		if containers, err := h.containers.List(); err == nil {
			resp.Host.ContainersTotal = len(containers)
			var totalHostCpu float64
			var totalContainerMem int64
			var maxContainerLimit int64

			for _, c := range containers {
				if c.Running {
					resp.Host.ContainersRunning++
				} else {
					resp.Host.ContainersStopped++
				}

				cm := ContainerMetric{
					ID:         c.ID,
					Name:       c.Name,
					Image:      c.Image,
					State:      c.State,
					Status:     c.Status,
					Project:    c.ProjectName,
					CPUPercent: c.CPU,
					NetIO:      c.Network,
					BlockIO:    c.BlockIO,
					PIDs:       c.PIDs,
				}

				if cm.CPUPercent != "" {
					cleaned := strings.TrimSpace(strings.ReplaceAll(cm.CPUPercent, "%", ""))
					if val, err := strconv.ParseFloat(cleaned, 64); err == nil {
						cm.CPUNum = val
						if c.Running {
							totalHostCpu += val
						}
					}
				}

				if c.Memory != "" {
					parts := strings.Split(c.Memory, "/")
					if len(parts) >= 2 {
						cm.MemUsage = strings.TrimSpace(parts[0])
						cm.MemLimit = strings.TrimSpace(parts[1])
						cm.MemBytes = parseBytes(cm.MemUsage)
						cm.MemLimitBytes = parseBytes(cm.MemLimit)
						if cm.MemLimitBytes > 0 {
							cm.MemPercent = float64(cm.MemBytes) / float64(cm.MemLimitBytes) * 100.0
						}
						if c.Running {
							totalContainerMem += cm.MemBytes
							if cm.MemLimitBytes > maxContainerLimit {
								maxContainerLimit = cm.MemLimitBytes
							}
						}
					} else {
						cm.MemUsage = strings.TrimSpace(c.Memory)
						cm.MemBytes = parseBytes(cm.MemUsage)
						if c.Running {
							totalContainerMem += cm.MemBytes
						}
					}
				}

				resp.Containers = append(resp.Containers, cm)
			}

			resp.Host.TotalCPUPercent = totalHostCpu

			// Fallback memory calculation if /proc/meminfo was unavailable
			if resp.Host.MemTotal == 0 && totalContainerMem > 0 {
				resp.Host.MemUsed = totalContainerMem
				if maxContainerLimit > 0 {
					resp.Host.MemTotal = maxContainerLimit
				} else {
					resp.Host.MemTotal = totalContainerMem
				}
				if resp.Host.MemTotal > 0 {
					resp.Host.MemFree = resp.Host.MemTotal - resp.Host.MemUsed
					resp.Host.MemPercent = float64(resp.Host.MemUsed) / float64(resp.Host.MemTotal) * 100.0
				}
			}
		}
	}

	// Query Cloudflare tunnel metrics if configured
	if h.cfg != nil && h.cfg.MetricsAddr != "" {
		client := &http.Client{Timeout: 1200 * time.Millisecond}
		cfResp, err := client.Get(h.cfg.MetricsAddr)
		if err == nil {
			defer cfResp.Body.Close()
			body, _ := io.ReadAll(cfResp.Body)
			raw := string(body)
			resp.Raw = raw

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

			reStatus := regexp.MustCompile(`cloudflared_tunnel_response_by_code\{status_code="(\d+)"\}\s+(\d+)`)
			for _, line := range strings.Split(raw, "\n") {
				if matches := reStatus.FindStringSubmatch(line); len(matches) > 2 {
					count, _ := strconv.ParseInt(matches[2], 10, 64)
					resp.ResponseCodes[matches[1]] = count
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func readLoadAvg() [3]float64 {
	var loads [3]float64
	data, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			loads[0], _ = strconv.ParseFloat(fields[0], 64)
			loads[1], _ = strconv.ParseFloat(fields[1], 64)
			loads[2], _ = strconv.ParseFloat(fields[2], 64)
		}
	}
	return loads
}

func readHostMemory() (total int64, used int64, free int64, pct float64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		var memTotal, memAvail, memFree int64
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				key := strings.TrimSuffix(parts[0], ":")
				val, _ := strconv.ParseInt(parts[1], 10, 64)
				switch key {
				case "MemTotal":
					memTotal = val * 1024
				case "MemAvailable":
					memAvail = val * 1024
				case "MemFree":
					memFree = val * 1024
				}
			}
		}
		if memTotal > 0 {
			if memAvail > 0 {
				used = memTotal - memAvail
			} else if memFree > 0 {
				used = memTotal - memFree
			}
			total = memTotal
			free = memTotal - used
			pct = float64(used) / float64(total) * 100.0
			return
		}
	}
	return 0, 0, 0, 0
}

func readUptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 1 {
			secs, _ := strconv.ParseFloat(fields[0], 64)
			return int64(secs)
		}
	}
	return 0
}

func readDiskSpace() (total int64, used int64, free int64, pct float64) {
	checkDir := "/app/data"
	if _, err := os.Stat(checkDir); err != nil {
		checkDir = "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// -P enforces POSIX single-line output format, avoiding line wraps on long mount names
	cmd := exec.CommandContext(ctx, "df", "-P", "-k", checkDir)
	out, err := cmd.Output()
	if err != nil {
		cmdRoot := exec.CommandContext(ctx, "df", "-P", "-k", "/")
		out, err = cmdRoot.Output()
	}

	if err == nil {
		return parseDiskOutput(string(out))
	}
	return
}

func parseDiskOutput(out string) (total int64, used int64, free int64, pct float64) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, 0, 0, 0
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 3 {
		return 0, 0, 0, 0
	}

	var totalIdx, usedIdx, freeIdx int
	if _, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
		// Long filesystem name wrapped onto previous line; fields[0] is total blocks
		totalIdx, usedIdx, freeIdx = 0, 1, 2
	} else if len(fields) >= 4 {
		// Standard layout: fields[0] is filesystem device name, fields[1] is total blocks
		totalIdx, usedIdx, freeIdx = 1, 2, 3
	} else {
		return 0, 0, 0, 0
	}

	if freeIdx < len(fields) {
		totalK, _ := strconv.ParseInt(fields[totalIdx], 10, 64)
		usedK, _ := strconv.ParseInt(fields[usedIdx], 10, 64)
		freeK, _ := strconv.ParseInt(fields[freeIdx], 10, 64)

		total = totalK * 1024
		used = usedK * 1024
		free = freeK * 1024
		if total > 0 {
			pct = float64(used) / float64(total) * 100.0
		}
	}
	return
}

func parseBytes(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	var numStr string
	var unitStr string
	for i, r := range s {
		if (r >= '0' && r <= '9') || r == '.' {
			numStr += string(r)
		} else {
			unitStr = strings.TrimSpace(s[i:])
			break
		}
	}
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(unitStr)
	switch {
	case strings.HasPrefix(unit, "TIB") || strings.HasPrefix(unit, "TB"):
		return int64(val * 1024 * 1024 * 1024 * 1024)
	case strings.HasPrefix(unit, "GIB") || strings.HasPrefix(unit, "GB"):
		return int64(val * 1024 * 1024 * 1024)
	case strings.HasPrefix(unit, "MIB") || strings.HasPrefix(unit, "MB"):
		return int64(val * 1024 * 1024)
	case strings.HasPrefix(unit, "KIB") || strings.HasPrefix(unit, "KB"):
		return int64(val * 1024)
	case strings.HasPrefix(unit, "B"):
		return int64(val)
	default:
		return int64(val)
	}
}
