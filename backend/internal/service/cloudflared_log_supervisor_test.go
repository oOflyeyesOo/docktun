package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"docktun/internal/config"
	"docktun/internal/ws"
)

func loggedMessages(t *testing.T, hub *ws.Hub) []string {
	t.Helper()
	var messages []string
	for len(hub.Broadcast) > 0 {
		var item map[string]string
		if err := json.Unmarshal(<-hub.Broadcast, &item); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, item["message"])
	}
	return messages
}

func TestCloudflareTimestampCursorSkipsReplayAndBoundsFragments(t *testing.T) {
	hub := ws.NewHub()
	service := &Cloudflared{hub: hub}
	first := time.Date(2026, 11, 23, 1, 2, 3, 0, time.UTC)
	second := first.Add(time.Second)
	cursor := service.streamTimestampedLogs(strings.NewReader(first.Format(time.RFC3339Nano)+" original\n"), time.Time{})
	cursor = service.streamTimestampedLogs(strings.NewReader(first.Format(time.RFC3339Nano)+" original\n"+second.Format(time.RFC3339Nano)+" fresh\n"+"docker CLI error\n"), cursor)
	messages := loggedMessages(t, hub)
	if strings.Join(messages, ",") != "original,fresh" || !cursor.Equal(second) {
		t.Fatalf("replayed or invalid logs: %v cursor %v", messages, cursor)
	}
	service.streamTimestampedLogs(strings.NewReader(second.Add(time.Second).Format(time.RFC3339Nano)+" "+strings.Repeat("x", 12000)+"\n"), cursor)
	messages = loggedMessages(t, hub)
	if len(messages) < 2 {
		t.Fatal("oversized log was not emitted in bounded fragments")
	}
	for _, message := range messages {
		if len(message) > 4096 {
			t.Fatalf("oversized fragment: %d", len(message))
		}
	}
}

func TestCloudflareSupervisorWaitsForRunningContainerAndResumes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Docker executable uses Unix shell")
	}
	directory := t.TempDir()
	calls := filepath.Join(directory, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\nprintf '2026-11-23T01:02:03Z original\\n2026-11-23T01:02:04Z fresh\\n'\n"
	if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inspections := 0
	hub := ws.NewHub()
	provider := &ProviderContainers{cfg: &config.Config{}, run: func(_ context.Context, args ...string) ([]byte, error) {
		inspections++
		c := updateContainer{ID: strings.Repeat("a", 64)}
		switch inspections {
		case 1:
			return nil, errors.New("No such container")
		case 2, 5:
			c.State.Running = false
		case 3, 4:
			c.State.Running = true
		default:
			cancel()
			return nil, errors.New("No such container")
		}
		return json.Marshal([]updateContainer{c})
	}}
	service := &Cloudflared{providers: provider, hub: hub}
	service.superviseLogs(ctx, time.Millisecond)
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	commands := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(commands) != 2 || !strings.Contains(commands[0], "--tail 50") || !strings.Contains(commands[1], "--since 2026-11-23T01:02:04Z") {
		t.Fatalf("unexpected log subscriptions: %q", raw)
	}
	if got := strings.Join(loggedMessages(t, hub), ","); got != "original,fresh" {
		t.Fatalf("log replay or missing-provider errors: %s", got)
	}
}
