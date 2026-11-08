package handler

import (
	"context"
	"docktun/internal/service"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAutomaticNotificationTransitions(t *testing.T) {
	tracker := notificationTracker{}
	cfg := notifConfig{Enabled: true, NotifyTunnelDown: true, NotifyContainerStop: true, NotifyHighUsage: true, CpuThresholdPercent: 85, MemoryThresholdPercent: 85}
	initial := service.NotificationSample{Containers: map[string]bool{"app": true, "tunnel": true}, Names: map[string]string{"app": "example", "tunnel": "cloudflared"}, UsageKnown: true}
	if events := tracker.observe(initial, "cloudflared", cfg); len(events) != 0 {
		t.Fatal(events)
	}
	stopped := service.NotificationSample{Containers: map[string]bool{"app": false, "tunnel": false}, Names: initial.Names, CPU: 90, Memory: 90, UsageKnown: true}
	events := tracker.observe(stopped, "cloudflared", cfg)
	if len(events) != 4 {
		t.Fatal(events)
	}
	if repeated := tracker.observe(stopped, "cloudflared", cfg); len(repeated) != 0 {
		t.Fatal("duplicate incidents", repeated)
	}
	if recovered := tracker.observe(initial, "cloudflared", cfg); len(recovered) != 4 {
		t.Fatal(recovered)
	}
	received := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received++
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("wrong content type")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	cfg.Type = "webhook"
	cfg.WebhookURL = server.URL
	if err := NewNotifications(t.TempDir()).sendIncident(context.Background(), cfg, incident{Kind: "container_stop", Message: "fixture stopped"}); err != nil {
		t.Fatal(err)
	}
	if received != 1 {
		t.Fatal(received)
	}
}

func TestMonitorRetriesWithoutDuplicatingStateTransitions(t *testing.T) {
	var delivered atomic.Int32
	complete := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delivered.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		select {
		case complete <- struct{}{}:
		default:
		}
	}))
	defer server.Close()
	h := NewNotifications(t.TempDir())
	cfg := notifConfig{Enabled: true, Type: "webhook", WebhookURL: server.URL, NotifyContainerStop: true}
	if err := h.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	samples := 0
	go func() {
		defer close(done)
		h.runMonitor(ctx, "cloudflared", func(context.Context) (service.NotificationSample, error) {
			samples++
			return service.NotificationSample{Containers: map[string]bool{"fixture": samples == 1}, Names: map[string]string{"fixture": "test-container"}}, nil
		}, 5*time.Millisecond)
	}()
	select {
	case <-complete:
	case <-time.After(time.Second):
		t.Fatal("failed alert was never retried")
	}
	cancel()
	<-done
	if delivered.Load() != 2 {
		t.Fatal("unexpected duplicate deliveries", delivered.Load())
	}
}
