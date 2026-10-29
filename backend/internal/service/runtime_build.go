package service

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// The installed image is identified by the running container, not a mutable tag
// that may already have been pulled for an update.
type RunningBuildInfo struct {
	Container string `json:"container,omitempty"`
	Image     string `json:"image,omitempty"`
	ImageID   string `json:"image_id,omitempty"`
	Revision  string `json:"revision,omitempty"`
	Error     string `json:"error,omitempty"`
}

var buildRevisionPattern = regexp.MustCompile(`^[a-fA-F0-9]{7,64}$`)

func inspectRunningBuild(ctx context.Context) (RunningBuildInfo, error) {
	c, err := selfContainer(ctx)
	if err != nil {
		return RunningBuildInfo{}, err
	}
	build := RunningBuildInfo{Container: strings.TrimPrefix(c.Name, "/"), ImageID: c.Image}
	build.Image, _ = c.Config["Image"].(string)
	var installed struct {
		ID     string `json:"Id"`
		Config struct{ Labels map[string]string }
	}
	if err = dockerJSON(ctx, "GET", "/images/"+url.PathEscape(c.Image)+"/json", nil, &installed); err != nil {
		return build, err
	}
	if installed.ID != c.Image {
		return build, fmt.Errorf("running image identity did not match")
	}
	revision := strings.TrimSpace(installed.Config.Labels["org.opencontainers.image.revision"])
	if buildRevisionPattern.MatchString(revision) {
		build.Revision = revision
	}
	return build, nil
}

func (s *SelfUpdater) runningBuild() RunningBuildInfo {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	ttl := time.Minute
	if s.build.Error != "" {
		ttl = 5 * time.Second
	}
	if !s.buildAt.IsZero() && time.Since(s.buildAt) < ttl {
		return s.build
	}
	inspect := s.inspectBuild
	if inspect == nil {
		inspect = inspectRunningBuild
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	build, err := inspect(ctx)
	if err != nil {
		build.Revision = ""
		build.Error = "Cannot verify the running image. Check Docker socket access and DOCKTUN_CONTAINER when using a custom hostname."
		if build.ImageID != "" {
			build.Error = "Running container found, but its image metadata could not be verified. Its tag may have changed or the image may no longer be available."
		}
	}
	s.build, s.buildAt = build, time.Now()
	return build
}
