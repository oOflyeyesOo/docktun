package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"docktun/internal/db"
)

func projectMountFixture(t *testing.T, svc *Projects, mounts string) *int {
	t.Helper()
	hostname, _ := os.Hostname()
	encoded, _ := json.Marshal(hostname)
	inspections := 0
	svc.runDocker = func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) != 2 || args[0] != "inspect" {
			return nil, fmt.Errorf("unexpected Docker command: %v", args)
		}
		inspections++
		return []byte(`[{"Config":{"Hostname":` + string(encoded) + `},"State":{"Running":true},"Mounts":` + mounts + `}]`), nil
	}
	return &inspections
}

func projectPathService(t *testing.T) *Projects {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return NewProjects(database, "")
}

func TestProjectHostPathMappingUsesMountBoundariesAndVisibleSource(t *testing.T) {
	for _, test := range []struct {
		name, directory, mounts, want string
		found, failed                 bool
	}{
		{"parent", "/volume1/docker/MediaStorm", `[{"Type":"bind","Source":"/volume1/docker","Destination":"/mounted-stacks","RW":true}]`, "/mounted-stacks/MediaStorm", true, false},
		{"exact", "/volume1/docker/MediaStorm", `[{"Type":"bind","Source":"/volume1/docker/MediaStorm","Destination":"/stack","RW":true}]`, "/stack", true, false},
		{"longest", "/volume1/docker/MediaStorm", `[{"Type":"bind","Source":"/volume1","Destination":"/volume","RW":true},{"Type":"bind","Source":"/volume1/docker","Destination":"/stacks","RW":true}]`, "/stacks/MediaStorm", true, false},
		{"prefix lookalike", "/volume1/docker-other/MediaStorm", `[{"Type":"bind","Source":"/volume1/docker","Destination":"/stacks","RW":true}]`, "", false, false},
		{"root source", "/volume1/docker/MediaStorm", `[{"Type":"bind","Source":"/","Destination":"/host","RW":true}]`, "/host/volume1/docker/MediaStorm", true, false},
		{"shadowed", "/volume1/docker/MediaStorm", `[{"Type":"bind","Source":"/volume1/docker","Destination":"/stacks","RW":true},{"Type":"bind","Source":"/unrelated","Destination":"/stacks/MediaStorm","RW":true}]`, "", true, true},
		{"tmpfs shadow", "/volume1/docker/MediaStorm", `[{"Type":"bind","Source":"/volume1/docker","Destination":"/stacks","RW":true},{"Type":"tmpfs","Destination":"/stacks/MediaStorm","RW":true}]`, "", true, true},
		{"no mount", "/volume1/docker/MediaStorm", `[]`, "", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var own updateContainer
			if err := json.Unmarshal([]byte(`{"Mounts":`+test.mounts+`}`), &own); err != nil {
				t.Fatal(err)
			}
			got, found, err := projectHostDirectory(test.directory, own)
			if found != test.found || (err != nil) != test.failed || err == nil && got.Path != test.want {
				t.Fatalf("mapping: %+v found=%v err=%v", got, found, err)
			}
		})
	}
}

func TestExistingHostLabelProjectReadsAndSavesMappedFiles(t *testing.T) {
	svc := projectPathService(t)
	mounted := t.TempDir()
	directory := filepath.Join(mounted, "MediaStorm")
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  app:\n    image: alpine\n"
	if err := os.WriteFile(filepath.Join(directory, "docker-compose.yml"), []byte(compose), 0644); err != nil {
		t.Fatal(err)
	}
	mounts, _ := json.Marshal([]map[string]any{{"Type": "bind", "Source": "/volume1/docker", "Destination": mounted, "RW": true}})
	inspections := projectMountFixture(t, svc, string(mounts))
	project, err := svc.Create("MediaStorm", "/volume1/docker/MediaStorm", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	filename, content, err := svc.GetComposeFile(project.ID)
	if err != nil || filename != "docker-compose.yml" || content != compose {
		t.Fatalf("Compose: %s %q %v", filename, content, err)
	}
	if *inspections != 1 {
		t.Fatalf("Compose lookup inspected self %d times", *inspections)
	}
	if err := svc.SaveComposeFile(project.ID, filename, compose+"# edited\n"); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(directory, filename)); err != nil || string(content) != compose+"# edited\n" {
		t.Fatal("write did not reach mounted stack", err)
	}
	if content, err := svc.GetEnvFile(project.ID); err != nil || content != "" {
		t.Fatalf("optional env: %q %v", content, err)
	}
	if err := svc.SaveEnvFile(project.ID, "VALUE=mapped\n"); err != nil {
		t.Fatal(err)
	}
	if content, err := svc.GetEnvFile(project.ID); err != nil || content != "VALUE=mapped\n" {
		t.Fatalf("mapped env: %q %v", content, err)
	}
	if err := svc.SaveFile(project.ID, "nested/marker", "mapped"); err != nil {
		t.Fatal(err)
	}
	if files, err := svc.ListFiles(project.ID, "nested"); err != nil || len(files) != 1 || files[0].Name != "marker" {
		t.Fatalf("mapped files: %+v %v", files, err)
	}
	if err := svc.DeleteFile(project.ID, "nested"); err != nil {
		t.Fatal(err)
	}
	saved, _ := svc.GetByID(project.ID)
	if saved.Path != project.Path {
		t.Fatal("file access rewrote stored host path")
	}
	access, err := svc.FileAccess(project.ID)
	if err != nil || !access.Available || !access.Writable || access.ResolvedPath != directory {
		t.Fatalf("mapped access: %+v %v", access, err)
	}
	if options, err := svc.DeleteOptions(project.ID); err != nil || options.CanRemoveFiles {
		t.Fatal("mapping widened permanent folder deletion policy")
	}
	// Both the selected stack and its mounted parent keep symlink boundaries.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetFile(project.ID, "escape/secret"); err == nil {
		t.Fatal("mapped read escaped stack root")
	}
	if err := os.Symlink(outside, filepath.Join(mounted, "outside-link")); err != nil {
		t.Fatal(err)
	}
	linked, err := svc.Create("linked", "/volume1/docker/outside-link", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetFile(linked.ID, "secret"); err == nil {
		t.Fatal("mapped project symlink escaped mounted parent")
	}
}

func TestReadOnlyProjectMountCannotBeChanged(t *testing.T) {
	svc := projectPathService(t)
	mounted := t.TempDir()
	if err := os.WriteFile(filepath.Join(mounted, "compose.yml"), []byte("services: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mounts, _ := json.Marshal([]map[string]any{{"Type": "bind", "Source": "/host/read-only", "Destination": mounted, "RW": false}})
	projectMountFixture(t, svc, string(mounts))
	project, _ := svc.Create("read-only", "/host/read-only", "", "", "")
	if _, _, err := svc.GetComposeFile(project.ID); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func() error{
		func() error { return svc.SaveFile(project.ID, "compose.yml", "changed") },
		func() error { return svc.CreateFile(project.ID, "new", true, "") },
		func() error { return svc.DeleteFile(project.ID, "compose.yml") },
	} {
		if err := change(); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatal("read-only mount mutation accepted", err)
		}
	}
	if access, err := svc.FileAccess(project.ID); err != nil || !access.Available || access.Writable || access.Reason == "" {
		t.Fatalf("read-only access: %+v %v", access, err)
	}
}

func TestUnavailableHostProjectNeverCreatesShadowFolders(t *testing.T) {
	svc := projectPathService(t)
	projectMountFixture(t, svc, `[]`)
	missing := filepath.Join(t.TempDir(), "host-only", "stack")
	project, _ := svc.Create("unmounted", missing, "", "", "")
	for _, operation := range []func() error{
		func() error { _, _, err := svc.GetComposeFile(project.ID); return err },
		func() error { return svc.SaveComposeFile(project.ID, "compose.yml", "services: {}") },
		func() error { return svc.SaveEnvFile(project.ID, "VALUE=no") },
		func() error { return svc.CreateFile(project.ID, "new", true, "") },
	} {
		if err := operation(); err == nil || !strings.Contains(err.Error(), "mount that host folder") {
			t.Fatal("missing mount not explained", err)
		}
	}
	svc.clearOutput(project.ID)
	svc.runComposeToBuffer(project.ID, project.Path, "up", "-d")
	lines, _ := svc.GetOutput(project.ID)
	if len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "mount that host folder") {
		t.Fatal("Compose action lost mount diagnostic", lines)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("created a container-only shadow host folder")
	}
	if access, err := svc.FileAccess(project.ID); err != nil || access.Available || access.Writable || access.Reason == "" {
		t.Fatalf("missing access: %+v %v", access, err)
	}
}

func TestExplicitManagedProjectCanCreateItsFolder(t *testing.T) {
	t.Chdir(t.TempDir())
	svc := projectPathService(t)
	projectMountFixture(t, svc, `[]`)
	project, _ := svc.Create("managed", filepath.Join("projects", "managed"), "", "", "")
	if err := svc.SaveComposeFile(project.ID, "compose.yml", "services: {}\n"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetComposeFile(project.ID); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join("projects", "escape")); err != nil {
		t.Fatal(err)
	}
	linked, _ := svc.Create("linked", filepath.Join("projects", "escape", "new-stack"), "", "", "")
	if err := svc.SaveComposeFile(linked.ID, "compose.yml", "services: {}\n"); err == nil {
		t.Fatal("missing managed stack was created through an escaping symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "new-stack")); !os.IsNotExist(err) {
		t.Fatal("managed creation escaped its root")
	}
}

func TestProjectResolutionSupportsAlreadyLocalMountAndNativePath(t *testing.T) {
	svc := projectPathService(t)
	mounted := t.TempDir()
	directory := filepath.Join(mounted, "existing")
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatal(err)
	}
	mounts, _ := json.Marshal([]map[string]any{{"Type": "bind", "Source": "/daemon/projects", "Destination": mounted, "RW": true}})
	projectMountFixture(t, svc, string(mounts))
	got, err := svc.resolveProjectDirectory(directory, false)
	if err != nil || got.Path != directory || got.Mount != mounted || got.ReadOnly {
		t.Fatalf("already-local folder: %+v %v", got, err)
	}
	svc.runDocker = nil // Simulate native development independently of the test runner socket.
	svc.requireMountInspection = func() bool { return false }
	got, err = svc.resolveProjectDirectory(directory, false)
	if err != nil || got.Path != directory {
		t.Fatalf("native folder: %+v %v", got, err)
	}
}

func TestConflictingHostAndContainerPathAliasesAreRejected(t *testing.T) {
	svc := projectPathService(t)
	projectMountFixture(t, svc, `[{"Type":"bind","Source":"/volume1/docker","Destination":"/volume1","RW":true}]`)
	if _, err := svc.resolveProjectDirectory("/volume1/docker/MediaStorm", false); err == nil || !strings.Contains(err.Error(), "matches both") {
		t.Fatal("ambiguous host path silently opened another folder", err)
	}
}

func TestContainerProjectAccessRequiresVerifiedSelfMounts(t *testing.T) {
	svc := projectPathService(t)
	shadow := t.TempDir()
	svc.runDocker = func(context.Context, ...string) ([]byte, error) {
		return nil, fmt.Errorf("self inspection unavailable")
	}
	if _, err := svc.resolveProjectDirectoryWithInspection(shadow, false, true); err == nil || !strings.Contains(err.Error(), "cannot verify DockTun's mounted stack paths") {
		t.Fatal("unverified container-only folder treated as host stack", err)
	}
	if directory, err := svc.resolveProjectDirectoryWithInspection(shadow, false, false); err != nil || directory.Path != shadow {
		t.Fatalf("native development lost direct filesystem access: %+v %v", directory, err)
	}
}

func TestGitUsesResolvedProjectWorkingDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	svc := projectPathService(t)
	mounted := t.TempDir()
	if out, err := exec.Command("git", "init", mounted).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	mounts, _ := json.Marshal([]map[string]any{{"Type": "bind", "Source": "/host/git-project", "Destination": mounted, "RW": true}})
	projectMountFixture(t, svc, string(mounts))
	if out, err := svc.runGit("/host/git-project", "rev-parse", "--show-toplevel"); err != nil || filepath.Clean(out) != filepath.Clean(mounted) {
		t.Fatalf("Git used host path: %q %v", out, err)
	}
}
