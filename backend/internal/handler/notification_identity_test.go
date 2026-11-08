package handler

import (
	"docktun/internal/service"
	"testing"
)

func TestNotificationIdentitySurvivesImageReplacement(t *testing.T) {
	old := stableNotificationSample(service.NotificationSample{Containers: map[string]bool{"old-id": true}, Names: map[string]string{"old-id": "app"}})
	next := stableNotificationSample(service.NotificationSample{Containers: map[string]bool{"new-id": true}, Names: map[string]string{"new-id": "app"}})
	tracker := notificationTracker{}
	cfg := notifConfig{Enabled: true, NotifyContainerStop: true}
	tracker.observe(old, "", cfg)
	if events := tracker.observe(next, "", cfg); len(events) != 0 {
		t.Fatal("healthy image replacement produced false outage", events)
	}
	stopped := stableNotificationSample(service.NotificationSample{Containers: map[string]bool{"new-id": false}, Names: map[string]string{"new-id": "app"}})
	if events := tracker.observe(stopped, "", cfg); len(events) != 1 || events[0].Key != "container:app" {
		t.Fatal(events)
	}
	if events := tracker.observe(next, "", cfg); len(events) != 1 || events[0].Kind != "recovery" {
		t.Fatal(events)
	}
}
