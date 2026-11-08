package handler

import (
	"bytes"
	"context"
	"docktun/internal/service"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type incident struct{ Kind, Key, Message string }
type notificationTracker struct {
	previous            *service.NotificationSample
	highCPU, highMemory bool
}

func (t *notificationTracker) observe(sample service.NotificationSample, tunnel string, cfg notifConfig) []incident {
	events := []incident{}
	if t.previous != nil {
		for id, running := range t.previous.Containers {
			if !running || sample.Containers[id] {
				continue
			}
			name := t.previous.Names[id]
			isTunnel := name == tunnel
			if (isTunnel && cfg.NotifyTunnelDown) || (!isTunnel && cfg.NotifyContainerStop) {
				kind := "container_stop"
				if isTunnel {
					kind = "tunnel_down"
				}
				events = append(events, incident{kind, id, "Container " + name + " stopped or was removed."})
			}
		}
	}
	if sample.UsageKnown {
		cpu, mem := sample.CPU >= float64(cfg.CpuThresholdPercent), sample.Memory >= float64(cfg.MemoryThresholdPercent)
		if cfg.NotifyHighUsage {
			if cpu && !t.highCPU {
				events = append(events, incident{"high_usage", "cpu", fmt.Sprintf("Docker workloads CPU usage is %.1f%% of host capacity.", sample.CPU)})
			}
			if mem && !t.highMemory {
				events = append(events, incident{"high_usage", "memory", fmt.Sprintf("Docker workloads memory usage is %.1f%% of host capacity.", sample.Memory)})
			}
			if !cpu && t.highCPU {
				events = append(events, incident{"recovery", "cpu", "Docker workloads CPU usage recovered below its threshold."})
			}
			if !mem && t.highMemory {
				events = append(events, incident{"recovery", "memory", "Docker workloads memory usage recovered below its threshold."})
			}
		}
		t.highCPU, t.highMemory = cpu, mem
	}
	if t.previous != nil {
		for id, running := range sample.Containers {
			if old, known := t.previous.Containers[id]; known && !old && running && ((sample.Names[id] == tunnel && cfg.NotifyTunnelDown) || (sample.Names[id] != tunnel && cfg.NotifyContainerStop)) {
				events = append(events, incident{"recovery", id, "Container " + sample.Names[id] + " is running again."})
			}
		}
	}
	copy := sample
	t.previous = &copy
	return events
}
func (h *Notifications) readConfig() (notifConfig, error) {
	raw, err := os.ReadFile(h.configFile())
	var cfg notifConfig
	if err == nil {
		err = json.Unmarshal(raw, &cfg)
	}
	if cfg.CpuThresholdPercent <= 0 {
		cfg.CpuThresholdPercent = 85
	}
	if cfg.MemoryThresholdPercent <= 0 {
		cfg.MemoryThresholdPercent = 85
	}
	return cfg, err
}
func (h *Notifications) sendIncident(ctx context.Context, cfg notifConfig, event incident) error {
	target := strings.TrimSpace(cfg.WebhookURL)
	payload := map[string]any{"event": event.Kind, "message": event.Message, "source": "DockTun", "timestamp": time.Now().UTC().Format(time.RFC3339)}
	contentType := "application/json"
	var raw []byte
	switch cfg.Type {
	case "discord":
		payload = map[string]any{"content": "DockTun: " + event.Message}
	case "slack":
		payload = map[string]any{"text": "DockTun: " + event.Message}
	case "telegram":
		if cfg.TelegramBotToken != "" && cfg.TelegramChatID != "" {
			target = "https://api.telegram.org/bot" + strings.TrimSpace(cfg.TelegramBotToken) + "/sendMessage"
			payload = map[string]any{"chat_id": strings.TrimSpace(cfg.TelegramChatID), "text": "DockTun: " + event.Message}
		} else {
			return fmt.Errorf("Telegram bot token and chat ID are required")
		}
	case "ntfy":
		contentType = "text/plain"
		raw = []byte(event.Message)
	}
	if target == "" {
		return fmt.Errorf("notification destination is missing")
	}
	if raw == nil {
		raw, _ = json.Marshal(payload)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid notification destination")
	}
	req.Header.Set("Content-Type", contentType)
	if cfg.Type == "ntfy" {
		req.Header.Set("Title", "DockTun "+event.Kind)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("notification destination could not be reached")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("notification destination returned HTTP %d", resp.StatusCode)
	}
	return nil
}
func (h *Notifications) DeliverEvent(kind, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cfg, err := h.readConfig()
	if err != nil || !cfg.Enabled || (kind == "deploy_finish" && !cfg.NotifyDeployFinish) {
		return
	}
	if err = h.sendIncident(ctx, cfg, incident{Kind: kind, Message: message}); err != nil {
		log.Printf("notification delivery failed: %v", err)
	}
}
func (h *Notifications) RunMonitor(ctx context.Context, tunnel string, sample func(context.Context) (service.NotificationSample, error)) {
	h.runMonitor(ctx, tunnel, sample, 30*time.Second)
}
func (h *Notifications) runMonitor(ctx context.Context, tunnel string, sample func(context.Context) (service.NotificationSample, error), period time.Duration) {
	tracker := notificationTracker{}
	pending := map[string]incident{}
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	check := func() {
		cfg, err := h.readConfig()
		if err != nil || !cfg.Enabled {
			tracker = notificationTracker{}
			pending = map[string]incident{}
			return
		}
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		state, err := sample(probe)
		cancel()
		if err != nil {
			return
		}
		for _, event := range tracker.observe(state, tunnel, cfg) {
			if event.Kind == "recovery" {
				delete(pending, "high_usage:"+event.Key)
				delete(pending, "container_stop:"+event.Key)
				delete(pending, "tunnel_down:"+event.Key)
			} else {
				delete(pending, "recovery:"+event.Key)
			}
			if len(pending) < 128 {
				pending[event.Kind+":"+event.Key] = event
			}
		}
		for key, event := range pending {
			if (event.Kind == "high_usage" && !cfg.NotifyHighUsage) || (event.Kind == "container_stop" && !cfg.NotifyContainerStop) || (event.Kind == "tunnel_down" && !cfg.NotifyTunnelDown) {
				delete(pending, key)
				continue
			}
			delivery, done := context.WithTimeout(ctx, 5*time.Second)
			err = h.sendIncident(delivery, cfg, event)
			done()
			if err == nil {
				delete(pending, key)
			} else {
				log.Printf("notification delivery failed; retrying next check: %v", err)
				break
			}
		}
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
