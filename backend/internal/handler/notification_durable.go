package handler

import (
	"bufio"
	"context"
	"docktun/internal/db"
	"docktun/internal/service"
	"encoding/json"
	"log"
	"os/exec"
	"time"
)

func (h *Notifications) WithDatabase(database *db.DB) *Notifications {
	h.queue = &service.AlertQueue{DB: database}
	h.wake = make(chan struct{}, 1)
	return h
}

type persistedTracker struct {
	Previous   *service.NotificationSample `json:"previous"`
	HighCPU    bool                        `json:"high_cpu"`
	HighMemory bool                        `json:"high_memory"`
}

func (h *Notifications) runDurableMonitor(ctx context.Context, tunnel string, sample func(context.Context) (service.NotificationSample, error), period time.Duration) {
	tracker := notificationTracker{}
	if raw, err := h.queue.State(); err == nil && raw != "" {
		var saved persistedTracker
		if json.Unmarshal([]byte(raw), &saved) == nil {
			tracker.previous, tracker.highCPU, tracker.highMemory = stableNotificationSamplePointer(saved.Previous), saved.HighCPU, saved.HighMemory
		}
	}
	events := make(chan struct{}, 1)
	go watchContainerChanges(ctx, events)
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	check := func(observe bool) {
		cfg, err := h.readConfig()
		if err != nil || !cfg.Enabled {
			return
		}
		if observe {
			probe, cancel := context.WithTimeout(ctx, 10*time.Second)
			state, err := sample(probe)
			cancel()
			if err != nil {
				return
			}
			state = stableNotificationSample(state)
			before := tracker
			events := tracker.observe(state, tunnel, cfg)
			pending := []service.QueuedIncident{}
			for _, event := range events {
				if name := state.Names[event.Key]; name != "" {
					event.Key = "container:" + name
				} else if before.previous != nil {
					if name := before.previous.Names[event.Key]; name != "" {
						event.Key = "container:" + name
					}
				}
				pending = append(pending, service.QueuedIncident{Kind: event.Kind, Key: event.Key, Message: event.Message})
			}
			raw, _ := json.Marshal(persistedTracker{tracker.previous, tracker.highCPU, tracker.highMemory})
			if err = h.queue.Apply(pending, string(raw), time.Now().Unix()); err != nil {
				tracker = before
				log.Printf("notification persistence failed: %v", err)
				return
			}
		}
		deliveries, err := h.queue.List(true, time.Now().Unix())
		if err != nil {
			return
		}
		for _, row := range deliveries {
			if (row.Kind == "container_stop" && !cfg.NotifyContainerStop) || (row.Kind == "tunnel_down" && !cfg.NotifyTunnelDown) || (row.Kind == "high_usage" && !cfg.NotifyHighUsage) || (row.Kind == "deploy_finish" && !cfg.NotifyDeployFinish) {
				continue
			}
			delivery, done := context.WithTimeout(ctx, 5*time.Second)
			err = h.sendIncident(delivery, cfg, incident{Kind: row.Kind, Key: row.Key, Message: row.Message})
			done()
			if persistErr := h.queue.Result(row.ID, err, time.Now().Unix()); persistErr != nil {
				log.Printf("notification result persistence failed: %v", persistErr)
				break
			}
		}
	}
	check(true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.wake:
			check(false)
		case <-events:
			check(true)
		case <-ticker.C:
			check(true)
		}
	}
}

// Events request a fresh inventory sample; periodic sampling and persisted state
// remain authoritative after disconnects. Bursts coalesce instead of spawning
// concurrent delivery loops. Very brief stop/start cycles may still coalesce.
func watchContainerChanges(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "docker", "events", "--filter", "type=container", "--filter", "event=die", "--filter", "event=start", "--filter", "event=destroy", "--filter", "event=health_status", "--format", "{{.Action}}")
		pipe, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err == nil {
			scanner := bufio.NewScanner(pipe)
			for scanner.Scan() {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
			_ = cmd.Wait()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func stableNotificationSamplePointer(sample *service.NotificationSample) *service.NotificationSample {
	if sample == nil {
		return nil
	}
	value := stableNotificationSample(*sample)
	return &value
}
func stableNotificationSample(sample service.NotificationSample) service.NotificationSample {
	containers := map[string]bool{}
	names := map[string]string{}
	for id, running := range sample.Containers {
		key := id
		if name := sample.Names[id]; name != "" {
			key = "container:" + name
		}
		containers[key] = running
		names[key] = sample.Names[id]
	}
	sample.Containers = containers
	sample.Names = names
	return sample
}
