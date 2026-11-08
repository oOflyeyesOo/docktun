package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type UpdateChannel struct {
	Channel  string `json:"channel"`
	Image    string `json:"image"`
	Override bool   `json:"override"`
	Signed   bool   `json:"signed_updates"`
}

func channelImage(channel string) string {
	switch channel {
	case "beta":
		return "ghcr.io/ooflyeyesoo/docktun:beta"
	case "edge":
		return "ghcr.io/ooflyeyesoo/docktun:edge"
	default:
		return defaultUpdateImage
	}
}
func (s *SelfUpdater) Channel() UpdateChannel {
	channel := "stable"
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(s.statePath), "update-channel.json"))
	if e == nil {
		var saved struct {
			Channel string `json:"channel"`
		}
		if json.Unmarshal(raw, &saved) == nil && (saved.Channel == "stable" || saved.Channel == "beta" || saved.Channel == "edge") {
			channel = saved.Channel
		}
	}
	image := channelImage(channel)
	override := strings.TrimSpace(os.Getenv("DOCKTUN_UPDATE_IMAGE"))
	if override != "" {
		image = override
	}
	return UpdateChannel{channel, image, override != "", strings.HasPrefix(image, "ghcr.io/ooflyeyesoo/docktun:") || strings.HasPrefix(image, "ghcr.io/ooflyeyesoo/docktun@")}
}
func (s *SelfUpdater) SaveChannel(channel string) error {
	if channel != "stable" && channel != "beta" && channel != "edge" {
		return fmt.Errorf("choose stable, beta or edge")
	}
	if updateInProgress(s.Status().Phase) {
		return fmt.Errorf("wait for the current update before changing channels")
	}
	file := filepath.Join(filepath.Dir(s.statePath), "update-channel.json")
	raw, _ := json.Marshal(map[string]string{"channel": channel})
	if err := os.WriteFile(file+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(file+".tmp", file)
}

// Verify the registry digest, not a mutable tag. Only DockTun's release/publish
// workflows and GitHub's OIDC issuer are trusted. Local/custom image overrides
// remain an explicit operator choice for offline development installations.
func verifyOfficialUpdate(ctx context.Context, image string) (string, error) {
	if !strings.HasPrefix(image, "ghcr.io/ooflyeyesoo/docktun:") && !strings.HasPrefix(image, "ghcr.io/ooflyeyesoo/docktun@") {
		return image, nil
	}
	var inspected struct{ RepoDigests []string }
	if err := dockerJSON(ctx, "GET", "/images/"+url.PathEscape(image)+"/json", nil, &inspected); err != nil {
		return "", err
	}
	digest := ""
	for _, ref := range inspected.RepoDigests {
		if strings.HasPrefix(ref, "ghcr.io/ooflyeyesoo/docktun@sha256:") {
			digest = ref
			break
		}
	}
	if digest == "" {
		return "", fmt.Errorf("official update has no immutable registry digest")
	}
	core, err := selfContainer(ctx)
	if err != nil {
		return "", err
	}
	_, err = updateDocker(ctx, "run", "--rm", "--network", "bridge", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=32m", "-e", "HOME=/tmp", "--entrypoint", "/usr/local/bin/cosign", core.Image, "verify", "--certificate-identity-regexp", `^https://github\.com/oOflyeyesOo/docktun/\.github/workflows/(release|publish)\.yml@refs/(tags/v[0-9][^/]*|heads/main)$`, "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", digest)
	if err != nil {
		return "", fmt.Errorf("official update signature could not be verified; no container was replaced. Legacy unsigned images require a reviewed signed release or an explicit custom image override")
	}
	return digest, nil
}
