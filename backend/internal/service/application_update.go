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
func replaceApplicationContainer(old updateContainer) (string, error) {
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
	backup := name + "-rollback-" + old.ID[:12]
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
				return "", fmt.Errorf("update failed: %v; replacement removal failed: %w; previous container retained as %s", cause, e, backup)
			}
		}
		if _, e := updateDocker(rb, "rename", old.ID, name); e != nil {
			return "", fmt.Errorf("update failed: %v; previous container retained as %s: %w", cause, backup, e)
		}
		if old.State.Running {
			if _, e := updateDocker(rb, "start", old.ID); e != nil {
				return "", fmt.Errorf("previous container restored but restart failed: %w", e)
			}
		}
		return "Previous container restored with its original data and networks.", fmt.Errorf("update rolled back: %w", cause)
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
		for time.Now().Before(deadline) {
			state, e := inspectUpdateContainer(ctx, newID)
			if e == nil && state.State.Running && !state.State.Restarting {
				if state.State.Health.Status == "healthy" {
					healthy = true
					break
				}
				if state.State.Health.Status == "unhealthy" {
					break
				}
				if state.State.Health.Status == "" {
					stable++
					if stable >= 3 {
						healthy = true
						break
					}
				}
			} else {
				break
			}
			time.Sleep(time.Second)
		}
		if !healthy {
			return rollback(fmt.Errorf("replacement failed its startup/health check"))
		}
	}
	if _, err = updateDocker(ctx, "rm", old.ID); err != nil {
		return "Replacement is active; previous container retained as " + backup, err
	}
	return "Updated image with original mounts, environment, entrypoint, restart policy, ports and network aliases preserved. Previous running/stopped state retained. Containers without a Docker healthcheck are checked for stable running state only.", nil
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
				if value == "" || value == old.ID || value == old.ID[:12] {
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
