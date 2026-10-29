package service

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestDockerControlBlankOutputKeepsUnderlyingFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := dockerCommand("stop", "unavailable-test-container")
	if err == nil || !errors.Is(err, exec.ErrNotFound) || !strings.Contains(err.Error(), "docker stop unavailable-test-container failed:") {
		t.Fatalf("Docker failure lost its underlying error: %v", err)
	}
}
