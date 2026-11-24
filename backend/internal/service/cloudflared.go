package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	logsOnce     sync.Once
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
	if !running && (parts[0] == "restarting" || parts[0] == "dead") {
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
	s.logsOnce.Do(func() { go s.superviseLogs(context.Background(), 2*time.Second) })
	return nil
}

func (s *Cloudflared) superviseLogs(ctx context.Context, retry time.Duration) {
	var containerID string
	var cursor time.Time
	for ctx.Err() == nil {
		inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		container, err := s.providers.inspect(inspectCtx, s.findContainer())
		cancel()
		// Disabled/missing providers are normal. Do not replay stopped logs or
		// broadcast Docker's missing-container error on every polling interval.
		if err == nil && container.State.Running && container.ID != "" {
			if container.ID != containerID {
				containerID = container.ID
				cursor = time.Time{}
			}
			args := []string{"logs", "-f", "--timestamps"}
			if cursor.IsZero() {
				args = append(args, "--tail", "50")
			} else {
				args = append(args, "--since", cursor.Format(time.RFC3339Nano))
			}
			args = append(args, containerID)
			cmd := exec.CommandContext(ctx, "docker", args...)
			stdout, pipeErr := cmd.StdoutPipe()
			if pipeErr == nil {
				cmd.Stderr = cmd.Stdout
				if err := cmd.Start(); err == nil {
					// Drain the pipe before Wait closes it. The cursor also prevents replay
					// when logs exits during a stop/start or a transient CLI interruption.
					cursor = s.streamTimestampedLogs(stdout, cursor)
					_ = cmd.Wait()
				}
			}
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Docker timestamps are the resume boundary, independent of the dashboard's
// clock. Read bounded fragments so an oversized application log line is safe.
func (s *Cloudflared) streamTimestampedLogs(r io.Reader, after time.Time) time.Time {
	reader := bufio.NewReaderSize(r, 4096)
	latest := after
	continuing, accepted := false, false
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			message := string(fragment)
			if !continuing {
				stamp, rest, found := strings.Cut(message, " ")
				timestamp, parseErr := time.Parse(time.RFC3339Nano, stamp)
				accepted = found && parseErr == nil && timestamp.After(after)
				if accepted {
					message = rest
					if timestamp.After(latest) {
						latest = timestamp
					}
				}
			}
			if accepted {
				s.broadcastLog(message)
			}
			continuing = err == bufio.ErrBufferFull
		}
		if err != nil && err != bufio.ErrBufferFull {
			return latest
		}
	}
}

func (s *Cloudflared) broadcastLog(message string) {
	message = strings.TrimSpace(message)
	if s.hub == nil || message == "" {
		return
	}
	entry, _ := json.Marshal(map[string]string{"time": time.Now().Format(time.RFC3339), "message": message})
	select {
	case s.hub.Broadcast <- entry:
	default:
	}
}

func (s *Cloudflared) StartTcpAccess(hostname, localURL string) (int, error) {
	return 0, errors.New("tcp access not supported in docker mode, use ingress config instead")
}

func (s *Cloudflared) StopTcpAccess(pid int) error {
	return errors.New("tcp access not supported in docker mode")
}
