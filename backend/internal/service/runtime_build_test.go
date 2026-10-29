package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunningBuildIgnoresSavedUpdateIdentityAndSharesInspection(t *testing.T) {
	updater := NewSelfUpdater(t.TempDir())
	if err := os.WriteFile(updater.statePath, []byte(`{"phase":"ready","running":{"revision":"ffffffffffffffffffffffffffffffffffffffff","image_id":"staged-image"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	updater.inspectBuild = func(ctx context.Context) (RunningBuildInfo, error) {
		calls++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("unbounded build inspection")
		}
		return RunningBuildInfo{Container: "docktun", ImageID: "installed-image", Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			state := updater.Status()
			if state.Running.ImageID != "installed-image" || state.Running.Revision != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
				t.Errorf("staged history shown as running: %#v", state.Running)
			}
		}()
	}
	group.Wait()
	if calls != 1 {
		t.Fatalf("repeated Docker inspections for concurrent status: %d", calls)
	}
	state := updater.Status()
	if err := updater.save(state); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(updater.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if _, exists := stored["running"]; exists {
		t.Fatal("live identity persisted as update history")
	}
}

func TestRunningBuildUnknownAndFailedInspectionAreExplicit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "local-build", true: "unavailable"}[fail], func(t *testing.T) {
			updater := NewSelfUpdater(t.TempDir())
			updater.inspectBuild = func(context.Context) (RunningBuildInfo, error) {
				if fail {
					return RunningBuildInfo{Revision: "old-label"}, errors.New("secret-host-path")
				}
				return RunningBuildInfo{ImageID: "local-image"}, nil
			}
			state := updater.Status()
			if state.Running.Revision != "" {
				t.Fatal("invented installed revision")
			}
			if fail && state.Running.Error == "" {
				t.Fatal("missing verification diagnostic")
			}
			if !fail && (state.Running.ImageID != "local-image" || state.Running.Error != "") {
				t.Fatal(state.Running)
			}
			raw, _ := json.Marshal(state)
			if strings.Contains(string(raw), "secret-host-path") {
				t.Fatal("inspection internals leaked")
			}
		})
	}
}

func TestRunningBuildMissingImageMetadataKeepsContainerIdentity(t *testing.T) {
	updater := NewSelfUpdater(t.TempDir())
	updater.inspectBuild = func(context.Context) (RunningBuildInfo, error) {
		return RunningBuildInfo{Container: "docktun", ImageID: "running-image", Revision: "stale-label"}, errors.New("image metadata unavailable")
	}
	build := updater.Status().Running
	if build.ImageID != "running-image" || build.Container != "docktun" || build.Revision != "" || !strings.Contains(build.Error, "image metadata") {
		t.Fatalf("missing image metadata confused with container identity: %#v", build)
	}
}
