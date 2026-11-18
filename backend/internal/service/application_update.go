package service

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"
)

var applicationUpdateLocks sync.Map

// Image updates preserve the running Docker definition. They never invent a
// one-service Compose stack or rewrite a database hostname, credential or mount.
func replaceApplicationWithReadiness(old updateContainer, readiness string, expected ...string) (string, error) {
	lock, _ := applicationUpdateLocks.LoadOrStore(strings.TrimPrefix(old.Name, "/"), &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	if !mu.TryLock() {
		return "", fmt.Errorf("this container is already being updated")
	}
	defer mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if err := validateUpdateContainer(old); err != nil {
		return "", err
	}
	if old.State.Paused || old.State.Restarting {
		return "", fmt.Errorf("wait until the container is neither paused nor restarting")
	}
	image, _ := old.Config["Image"].(string)
	if image == "" {
		return "", fmt.Errorf("container image is missing")
	}
	if err := stageUpdateImage(ctx, image); err != nil {
		return "", fmt.Errorf("image was not changed; for a local source build use the stack build/deploy action: %w", err)
	}
	raw, err := updateDocker(ctx, "image", "inspect", image, "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	imageID := strings.TrimSpace(string(raw))
	if len(expected) > 0 && expected[0] != "" && expected[0] != imageID {
		return "", fmt.Errorf("candidate image changed; review a fresh update plan")
	}
	if err = validateDatabaseImageTransition(ctx, old, imageID); err != nil {
		return "", err
	}
	if imageID == old.Image {
		return "Container already uses the current image; settings and data were unchanged.", nil
	}
	// Refuse an out-of-date snapshot before touching the original.
	current, err := inspectUpdateContainer(ctx, old.ID)
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(current.Config, old.Config) || !reflect.DeepEqual(current.HostConfig, old.HostConfig) || !reflect.DeepEqual(current.NetworkSettings, old.NetworkSettings) || current.State.Running != old.State.Running {
		return "", fmt.Errorf("container changed during image preparation; reload and retry")
	}
	name := strings.TrimPrefix(old.Name, "/")
	shortID := old.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	backup := name + "-rollback-" + shortID
	body := replacementCreateBody(old, imageID, false)
	if old.State.Running {
		if _, err = updateDocker(ctx, "stop", "--time", "30", old.ID); err != nil {
			return "", err
		}
	}
	if _, err = updateDocker(ctx, "rename", old.ID, backup); err != nil {
		if old.State.Running {
			_, _ = updateDocker(ctx, "start", old.ID)
		}
		return "", err
	}
	newID := ""
	rollback := func(cause error) (string, error) {
		rb, done := context.WithTimeout(context.Background(), 90*time.Second)
		defer done()
		if newID != "" {
			if _, e := updateDocker(rb, "rm", "-f", newID); e != nil {
				return "", &operationRecoveryRequired{fmt.Errorf("canary auto-rollback failed: %v; replacement removal failed: %w; previous container retained as %s", cause, e, backup)}
			}
		}
		if _, e := updateDocker(rb, "rename", old.ID, name); e != nil {
			return "", &operationRecoveryRequired{fmt.Errorf("canary auto-rollback failed: %v; previous container retained as %s: %w", cause, backup, e)}
		}
		if old.State.Running {
			if _, e := updateDocker(rb, "start", old.ID); e != nil {
				return "", &operationRecoveryRequired{fmt.Errorf("canary auto-rollback: previous container restored but restart failed: %w", e)}
			}
		}
		return "Canary auto-rollback completed: new container failed startup verification. Previous container restored with original data, networks, and configuration intact.", fmt.Errorf("canary auto-rollback: %w", cause)
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err = dockerJSON(ctx, "POST", "/containers/create?name="+url.QueryEscape(name), body, &created); err != nil {
		return rollback(err)
	}
	newID = created.ID
	replacement, err := inspectUpdateContainer(ctx, newID)
	if err != nil {
		return rollback(err)
	}
	if err = verifyReplacementStorageNetworks(old, replacement); err != nil {
		return rollback(err)
	}
	if old.State.Running {
		if _, err = updateDocker(ctx, "start", newID); err != nil {
			return rollback(err)
		}
		deadline := time.Now().Add(90 * time.Second)
		stable := 0
		healthy := false
		var failureCause error
		for time.Now().Before(deadline) {
			state, e := inspectUpdateContainer(ctx, newID)
			if e != nil {
				failureCause = fmt.Errorf("canary inspection probe error: %w", e)
				break
			}
			if state.State.Restarting {
				failureCause = fmt.Errorf("new container entered a crash / restart loop")
				break
			}
			if !state.State.Running {
				failureCause = fmt.Errorf("new container exited unexpectedly on startup with exit code %d", state.State.ExitCode)
				break
			}
			if state.State.Health.Status == "unhealthy" {
				failureCause = fmt.Errorf("Docker health check reported unhealthy status")
				break
			}
			if state.State.Health.Status == "healthy" {
				healthy = true
				break
			}
			if state.State.Health.Status == "" {
				stable++
				if stable >= 10 {
					healthy = true
					break
				}
			}
			time.Sleep(time.Second)
		}
		if !healthy {
			if failureCause == nil {
				failureCause = fmt.Errorf("canary check timed out: container failed to reach healthy running state within 90 seconds")
			}
			return rollback(failureCause)
		}
	}
	if old.State.Running && readiness != "" {
		deadline := time.Now().Add(90 * time.Second)
		for {
			err = probeReadiness(ctx, readiness)
			if err == nil {
				break
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				return rollback(fmt.Errorf("readiness probe failed on %s: %w", readiness, err))
			}
			select {
			case <-ctx.Done():
				return rollback(fmt.Errorf("readiness probe canceled: %w", ctx.Err()))
			case <-time.After(2 * time.Second):
			}
		}

	}
	if _, err = updateDocker(ctx, "rm", old.ID); err != nil {
		return "Replacement is active; previous container retained as " + backup, &operationRecoveryRequired{err}
	}
	return "Updated image with original mounts, environment, entrypoint, restart policy, ports and network aliases preserved. Canary verification passed. Previous running/stopped state retained.", nil
}
func verifyReplacementStorageNetworks(old, next updateContainer) error {
	for _, mount := range old.Mounts {
		if mount.Type == "tmpfs" {
			continue
		}
		found := false
		for _, actual := range next.Mounts {
			if mount.Destination == actual.Destination && mount.Type == actual.Type && mount.RW == actual.RW && ((mount.Type == "volume" && mount.Name == actual.Name) || (mount.Type != "volume" && mount.Source == actual.Source)) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("replacement would change persistent mount %s", mount.Destination)
		}
	}
	for name, ep := range old.NetworkSettings.Networks {
		actual, ok := next.NetworkSettings.Networks[name]
		if !ok {
			return fmt.Errorf("replacement lost network %s", name)
		}
		if aliases, ok := ep["Aliases"].([]any); ok {
			for _, alias := range aliases {
				value, _ := alias.(string)
				shortOldID := old.ID
				if len(shortOldID) > 12 {
					shortOldID = shortOldID[:12]
				}
				if value == "" || value == old.ID || value == shortOldID {
					continue
				}
				found := false
				for _, candidate := range interfaceStrings(actual["Aliases"]) {
					if candidate == value {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("replacement lost alias %s on %s", value, name)
				}
			}
		}
	}
	return nil
}
func interfaceStrings(raw any) []string {
	result := []string{}
	switch values := raw.(type) {
	case []any:
		for _, v := range values {
			if text, ok := v.(string); ok {
				result = append(result, text)
			}
		}
	case []string:
		return values
	}
	return result
}
