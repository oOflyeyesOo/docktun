package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCheckDetectsLocalBuildViaUntaggedSHA(t *testing.T) {
	updater := NewSelfUpdater(t.TempDir())
	updater.selfContainer = func(ctx context.Context) (updateContainer, error) {
		return updateContainer{
			ID:    "c1234567890ab",
			Name:  "/docktun",
			Image: "sha256:956f684565cba1b2c3d4e5f67890123456789012345678901234567890123456",
			Config: map[string]any{
				"Image": "956f684565cb",
			},
		}, nil
	}
	updater.stageImage = func(ctx context.Context, image string) error {
		t.Fatal("stageImage should not be called for local build")
		return nil
	}

	state, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for local build check, got: %v", err)
	}
	if !state.LocalBuild {
		t.Errorf("expected state.LocalBuild to be true, got: false")
	}
	if state.Phase != "ready" {
		t.Errorf("expected state.Phase to be 'ready', got: %s", state.Phase)
	}
	if !state.Checked {
		t.Errorf("expected state.Checked to be true, got: false")
	}
	if state.Available {
		t.Errorf("expected state.Available to be false for local build, got: true")
	}
	wantMsg := "Running a local build or source checkout. Updates are managed via git pull & compose rebuild, or switch channel to official release."
	if state.Message != wantMsg {
		t.Errorf("unexpected state.Message:\ngot:  %s\nwant: %s", state.Message, wantMsg)
	}

	// Verify status preserves local build info
	status := updater.Status()
	if !status.LocalBuild {
		t.Errorf("expected status.LocalBuild to be true")
	}
}

func TestCheckDetectsLocalBuildViaCustomLocalTag(t *testing.T) {
	updater := NewSelfUpdater(t.TempDir())
	updater.selfContainer = func(ctx context.Context) (updateContainer, error) {
		return updateContainer{
			ID:    "c9876543210ab",
			Name:  "/docktun",
			Image: "sha256:1111222233334444555566667777888899990000aaaaabbbbbcccccdddddeeeee",
			Config: map[string]any{
				"Image": "docktun:latest",
			},
		}, nil
	}

	state, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !state.LocalBuild {
		t.Errorf("expected state.LocalBuild to be true for docktun:latest")
	}
	if state.Phase != "ready" {
		t.Errorf("expected state.Phase to be 'ready', got: %s", state.Phase)
	}
}

func TestCheckDockerSocketPermissionDeniedDiagnostic(t *testing.T) {
	updater := NewSelfUpdater(t.TempDir())
	updater.selfContainer = func(ctx context.Context) (updateContainer, error) {
		return updateContainer{}, errors.New("dial unix /var/run/docker.sock: connect: permission denied")
	}

	state, err := updater.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for socket permission denied")
	}
	if state.Phase != "failed" {
		t.Errorf("expected state.Phase to be 'failed', got: %s", state.Phase)
	}
	if !state.Checked {
		t.Errorf("expected state.Checked to be true, got: false")
	}
	if !strings.Contains(state.Error, "Docker socket permission denied") {
		t.Errorf("expected rich permission error, got: %s", state.Error)
	}
	if !strings.Contains(state.Error, "/var/run/docker.sock") {
		t.Errorf("expected error to mention /var/run/docker.sock, got: %s", state.Error)
	}
}

func TestCheckRegistryPullTimeoutDiagnostic(t *testing.T) {
	updater := NewSelfUpdater(t.TempDir())
	updater.selfContainer = func(ctx context.Context) (updateContainer, error) {
		return updateContainer{
			ID:    "cOfficialContainer1",
			Name:  "/docktun",
			Image: "sha256:officialoldhash1234567890",
			Config: map[string]any{
				"Image": "ghcr.io/ooflyeyesoo/docktun:latest",
			},
		}, nil
	}
	updater.stageImage = func(ctx context.Context, image string) error {
		return errors.New("net/http: request canceled while waiting for connection (Client.Timeout exceeded while awaiting headers)")
	}

	state, err := updater.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for registry pull timeout")
	}
	if state.Phase != "failed" {
		t.Errorf("expected state.Phase to be 'failed', got: %s", state.Phase)
	}
	if !state.Checked {
		t.Errorf("expected state.Checked to be true, got: false")
	}
	if !strings.Contains(state.Error, "registry pull timed out") {
		t.Errorf("expected rich pull timeout message, got: %s", state.Error)
	}
}

func TestCheckCosignVerificationFailureDiagnostic(t *testing.T) {
	tests := []struct {
		name        string
		verifyErr   error
		wantSnippet string
	}{
		{
			name:        "CosignBinaryAbsent",
			verifyErr:   errors.New("executable file not found in $PATH: cosign"),
			wantSnippet: "Cosign binary is absent",
		},
		{
			name:        "OIDCUnreachable",
			verifyErr:   errors.New("dial tcp: lookup token.actions.githubusercontent.com: i/o timeout"),
			wantSnippet: "Cosign verification could not reach certificate issuer",
		},
		{
			name:        "NoImmutableDigest",
			verifyErr:   errors.New("official update has no immutable registry digest"),
			wantSnippet: "official update has no immutable registry digest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updater := NewSelfUpdater(t.TempDir())
			updater.selfContainer = func(ctx context.Context) (updateContainer, error) {
				return updateContainer{
					ID:    "cOfficialContainer1",
					Name:  "/docktun",
					Image: "sha256:officialoldhash1234567890",
					Config: map[string]any{
						"Image": "ghcr.io/ooflyeyesoo/docktun:latest",
					},
				}, nil
			}
			updater.stageImage = func(ctx context.Context, image string) error {
				return nil
			}
			updater.verifyUpdate = func(ctx context.Context, image string) (string, error) {
				return "", tt.verifyErr
			}

			state, err := updater.Check(context.Background())
			if err == nil {
				t.Fatal("expected error from verifyUpdate")
			}
			if state.Phase != "failed" {
				t.Errorf("expected state.Phase to be 'failed', got: %s", state.Phase)
			}
			if !state.Checked {
				t.Errorf("expected state.Checked to be true, got: false")
			}
			if !strings.Contains(state.Error, tt.wantSnippet) {
				t.Errorf("expected state.Error to contain %q, got: %s", tt.wantSnippet, state.Error)
			}
		})
	}
}

func TestCheckOfficialUpdateSuccessAndAvailability(t *testing.T) {
	updater := NewSelfUpdater(t.TempDir())
	updater.selfContainer = func(ctx context.Context) (updateContainer, error) {
		return updateContainer{
			ID:    "cOfficialContainer1",
			Name:  "/docktun",
			Image: "sha256:oldimage12345",
			Config: map[string]any{
				"Image": "ghcr.io/ooflyeyesoo/docktun:latest",
			},
		}, nil
	}
	updater.stageImage = func(ctx context.Context, image string) error {
		return nil
	}
	updater.verifyUpdate = func(ctx context.Context, image string) (string, error) {
		return "ghcr.io/ooflyeyesoo/docktun@sha256:newverifiedhash", nil
	}
	updater.inspectImage = func(ctx context.Context, image string) (string, error) {
		return "sha256:newimage67890", nil
	}

	state, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != "ready" {
		t.Errorf("expected ready phase, got: %s", state.Phase)
	}
	if !state.Checked {
		t.Errorf("expected checked to be true")
	}
	if !state.Available {
		t.Errorf("expected available to be true when staged image differs from running container")
	}
	if state.LocalBuild {
		t.Errorf("expected official build to not be marked as local")
	}
}
