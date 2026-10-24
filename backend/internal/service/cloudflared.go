package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
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
	cfg          *config.Config
	hub          *ws.Hub
	mu           sync.RWMutex
	cachedVer    string
	cachedMem    string
	lastMemCheck time.Time
}

func NewCloudflared(cfg *config.Config, hub *ws.Hub) *Cloudflared {
	return &Cloudflared{cfg: cfg, hub: hub}
}

func (s *Cloudflared) findContainer() string {
	name := s.cfg.CloudflaredContainer
	if name == "" {
		name = "docktun-cloudflared"
	}
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", name).Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return name
	}
	// Fallback: look for any container running cloudflared
	psOut, err := exec.Command("docker", "ps", "-a", "--filter", "ancestor=cloudflare/cloudflared", "--format", "{{.Names}}").Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(psOut)), "\n")
		if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
			return strings.Fields(lines[0])[0]
		}
	}
	return name
}

func (s *Cloudflared) IsRunning() bool {
	return s.Status().Running
}

func (s *Cloudflared) Status() ProcessState {
	mesh, err := NewMeshService(filepath.Dir(s.cfg.DBPath), nil, s.cfg).Load()
	if err != nil {
		return ProcessState{Status: "configuration error", LastError: err.Error()}
	}
	if mesh.Mode != "public" {
		return ProcessState{Status: "disabled"}
	}
	cName := s.findContainer()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}} {{.State.StartedAt}}", cName).Output()
	if err != nil {
		return ProcessState{Running: false, Status: "not found", LastError: fmt.Sprintf("Cloudflared container '%s' was not found", cName)}
	}

	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return ProcessState{Running: false, Status: "unknown"}
	}

	status := parts[0]
	running := status == "running"

	var uptime string
	if running {
		if t, err := time.Parse(time.RFC3339Nano, parts[1]); err == nil {
			uptime = time.Since(t).Truncate(time.Second).String()
		}
	}

	if s.cachedVer == "" {
		s.cachedVer = s.getVersion()
	}

	if time.Since(s.lastMemCheck) > 30*time.Second {
		go func() {
			s.mu.Lock()
			s.cachedMem = s.getMemory()
			s.lastMemCheck = time.Now()
			s.mu.Unlock()
		}()
	}

	s.mu.RLock()
	mem := s.cachedMem
	s.mu.RUnlock()

	var lastErr string
	if !running {
		logOut, _ := exec.Command("docker", "logs", "--tail", "6", cName).CombinedOutput()
		lastErr = strings.TrimSpace(string(logOut))
	}

	return ProcessState{
		Running:     running,
		StartTime:   parts[1],
		Uptime:      uptime,
		Status:      status,
		Version:     s.cachedVer,
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
	cmd := exec.Command("docker", action, cName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed on %s: %s", action, cName, strings.TrimSpace(string(out)))
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
