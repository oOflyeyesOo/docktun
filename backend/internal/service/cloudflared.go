package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"docktun/internal/config"
	"docktun/internal/ws"
)

type ProcessState struct {
	Running     bool   `json:"running"`
	StartTime   string `json:"start_time,omitempty"`
	Uptime      string `json:"uptime,omitempty"`
	Status      string `json:"status"`
	Version     string `json:"version,omitempty"`
	MemoryUsage string `json:"memory_usage,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

type Cloudflared struct {
	providers    *ProviderContainers
	cfg          *config.Config
	hub          *ws.Hub
	mu           sync.RWMutex
	cachedVer    string
	cachedMem    string
	lastMemCheck time.Time
}

func NewCloudflared(cfg *config.Config, hub *ws.Hub) *Cloudflared {
	return &Cloudflared{cfg: cfg, hub: hub, providers: NewProviderContainers(cfg)}
}

func (s *Cloudflared) findContainer() string {
	return s.providers.name("cloudflare")
}

func (s *Cloudflared) IsRunning() bool {
	return s.Status().Running
}

func (s *Cloudflared) Status() ProcessState {
	cName := s.findContainer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := s.providers.run(ctx, "inspect", "-f", "{{.State.Status}} {{.State.StartedAt}} {{.State.ExitCode}}", cName)
	if err != nil {
		return ProcessState{Running: false, Status: "not installed", LastError: fmt.Sprintf("Cloudflare container %s is missing. Enable Cloudflare to create it.", cName)}
	}

	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return ProcessState{Running: false, Status: "unknown"}
	}

	status := parts[0]
	if status == "created" || status == "exited" {
		status = "stopped"
	}
	running := status == "running"

	var uptime string
	if running {
		if t, err := time.Parse(time.RFC3339Nano, parts[1]); err == nil {
			uptime = time.Since(t).Truncate(time.Second).String()
		}
	}

	s.mu.Lock()
	if running && s.cachedVer == "" {
		s.cachedVer = s.getVersion()
	}
	version := s.cachedVer
	if running && time.Since(s.lastMemCheck) > 30*time.Second {
		s.lastMemCheck = time.Now()
		go func() { memory := s.getMemory(); s.mu.Lock(); s.cachedMem = memory; s.mu.Unlock() }()
	}
	mem := s.cachedMem
	s.mu.Unlock()

	var lastErr string
	if !running && parts[0] != "created" && (parts[0] == "restarting" || (len(parts) > 2 && parts[2] != "0")) {
		status = "failed"
		logOut, _ := exec.Command("docker", "logs", "--tail", "6", cName).CombinedOutput()
		lastErr = strings.TrimSpace(string(logOut))
	}

	return ProcessState{
		Running:     running,
		StartTime:   parts[1],
		Uptime:      uptime,
		Status:      status,
		Version:     version,
		MemoryUsage: mem,
		LastError:   lastErr,
	}
}

func (s *Cloudflared) getVersion() string {
	cName := s.findContainer()
	out, err := exec.Command("docker", "exec", cName, "cloudflared", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Cloudflared) getMemory() string {
	cName := s.findContainer()
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", cName).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Cloudflared) Start() error {
	return s.dockerCmd("start")
}

func (s *Cloudflared) Stop() error {
	return s.dockerCmd("stop")
}

func (s *Cloudflared) Restart() error {
	return s.dockerCmd("restart")
}

func (s *Cloudflared) dockerCmd(action string) error {
	cName := s.findContainer()
	if err := s.providers.Control("cloudflare", action); err != nil {
		return err
	}

	entry, _ := json.Marshal(map[string]string{
		"time":    time.Now().Format(time.RFC3339),
		"message": fmt.Sprintf("[docktun] %s %s", cName, action),
	})
	if s.hub != nil {
		s.hub.Broadcast <- entry
	}

	return nil
}

func (s *Cloudflared) Logs() error {
	cName := s.findContainer()
	cmd := exec.Command("docker", "logs", "-f", "--tail", "100", cName)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return err
	}

	go s.streamToHub(stdout)
	go s.streamToHub(stderr)
	go cmd.Wait()

	return nil
}

func (s *Cloudflared) streamToHub(r interface{ Read([]byte) (int, error) }) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			entry, _ := json.Marshal(map[string]string{
				"time":    time.Now().Format(time.RFC3339),
				"message": strings.TrimSpace(string(buf[:n])),
			})
			s.hub.Broadcast <- entry
		}
		if err != nil {
			return
		}
	}
}

func (s *Cloudflared) StartTcpAccess(hostname, localURL string) (int, error) {
	return 0, errors.New("tcp access not supported in docker mode, use ingress config instead")
}

func (s *Cloudflared) StopTcpAccess(pid int) error {
	return errors.New("tcp access not supported in docker mode")
}
