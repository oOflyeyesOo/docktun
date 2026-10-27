package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"docktun/internal/db"
)

func TestDeleteProject(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("Init db failed: %v", err)
	}
	defer database.Close()

	selfPath := filepath.Join(tmpDir, "self")
	pSvc := NewProjects(database, selfPath)

	managedRoot, err := filepath.Abs("projects")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(managedRoot, 0755); err != nil {
		t.Fatal(err)
	}
	projDir, err := os.MkdirTemp(managedRoot, "delete-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(projDir); os.Remove(managedRoot) })
	_ = os.MkdirAll(projDir, 0755)
	composeFile := filepath.Join(projDir, "docker-compose.yml")
	_ = os.WriteFile(composeFile, []byte("services:\n  app:\n    image: alpine\n"), 0644)

	proj, err := pSvc.Create("my-app", projDir, "", "", "")
	if err != nil {
		t.Fatalf("Create project failed: %v", err)
	}

	// Delete with removeFiles = true
	err = pSvc.DeleteProject(proj.ID, false, true)
	if err != nil {
		t.Fatalf("DeleteProject failed: %v", err)
	}

	// Verify project row is deleted from database
	_, err = pSvc.GetByID(proj.ID)
	if err == nil {
		t.Errorf("expected project row to be deleted")
	}

	// Verify files removed from disk
	if _, err := os.Stat(projDir); !os.IsNotExist(err) {
		t.Errorf("expected directory %s to be removed", projDir)
	}
}

func TestDeleteStackWithoutHostComposeDirectory(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "/opt/projects/docktun")
	project, err := svc.Create("example", "/volume1/docker/example", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	svc.runDocker = func(ctx context.Context, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch args[0] {
		case "ps":
			return []byte("abc123\ndef456"), nil
		case "inspect":
			return []byte(`[{"Id":"abc123","Name":"/example-web-1","Config":{"Labels":{"com.docker.compose.project":"example"}}},{"Id":"def456","Name":"/example-db-1","Config":{"Labels":{"com.docker.compose.project":"example"}}}]`), nil
		case "network":
			if args[1] == "ls" {
				return []byte("network123"), nil
			}
		}
		return nil, nil
	}
	if err := svc.DeleteProject(project.ID, true, false); err != nil {
		t.Fatal(err)
	}
	expected := []string{"ps -aq --no-trunc --filter label=com.docker.compose.project=example", "inspect abc123 def456", "stop --time 10 abc123 def456", "rm abc123 def456", "network ls -q --filter label=com.docker.compose.project=example", "network rm network123"}
	if strings.Join(calls, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("unexpected operations: %v", calls)
	}
	if _, err := svc.GetByID(project.ID); err == nil {
		t.Fatal("stack record survived successful deletion")
	}
}

func TestDeleteStackFailuresKeepRecordAndFiles(t *testing.T) {
	for _, failure := range []string{"ps", "inspect", "stop", "rm", "network"} {
		t.Run(failure, func(t *testing.T) {
			database, err := db.New(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			svc := NewProjects(database, "")
			project, err := svc.Create("example", t.TempDir(), "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			svc.runDocker = func(ctx context.Context, args ...string) ([]byte, error) {
				if args[0] == failure {
					return nil, fmt.Errorf("test Docker failure")
				}
				if args[0] == "ps" {
					return []byte("abc123"), nil
				}
				if args[0] == "inspect" {
					return []byte(`[{"Id":"abc123","Name":"/example-web-1","Config":{"Labels":{"com.docker.compose.project":"example"}}}]`), nil
				}
				return nil, nil
			}
			if err := svc.DeleteProject(project.ID, true, false); err == nil {
				t.Fatal("failure was swallowed")
			}
			if _, err := svc.GetByID(project.ID); err != nil {
				t.Fatal("failed deletion removed stack record")
			}
			if _, err := os.Stat(project.Path); err != nil {
				t.Fatal("failed deletion removed project files")
			}
		})
	}
}

func TestDeleteStackProtectsUnrelatedAndSelfContainers(t *testing.T) {
	for _, inspection := range []string{
		`[{"Id":"abc123","Name":"/other","Config":{"Labels":{"com.docker.compose.project":"other"}}}]`,
		`[{"Id":"abc123","Name":"/docktun","Config":{"Labels":{"com.docker.compose.project":"example"}}}]`,
		`not json`,
	} {
		database, err := db.New(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		svc := NewProjects(database, "")
		p, err := svc.Create("example", "/host-only/example", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		svc.runDocker = func(ctx context.Context, args ...string) ([]byte, error) {
			if args[0] == "ps" {
				return []byte("abc123"), nil
			}
			if args[0] == "inspect" {
				return []byte(inspection), nil
			}
			t.Fatalf("unsafe command: %v", args)
			return nil, nil
		}
		if err := svc.DeleteProject(p.ID, true, false); err == nil {
			t.Fatal("unsafe deletion allowed")
		}
		database.Close()
	}
}

func TestProjectFileRemovalRejectsUnsafePathsBeforeDocker(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "/opt/projects/docktun")
	svc.runDocker = func(ctx context.Context, args ...string) ([]byte, error) {
		t.Fatal("Docker called before validating files")
		return nil, nil
	}
	for _, path := range []string{"/", ".", "/opt/projects", "/opt/projects/docktun", "/volume1/docker/unknown", t.TempDir()} {
		p, err := svc.Create("example", path, "", "", "")
		if err != nil {
			continue
		}
		if err := svc.DeleteProject(p.ID, true, true); err == nil {
			t.Fatalf("unsafe cleanup allowed: %s", path)
		}
		if _, err := svc.GetByID(p.ID); err != nil {
			t.Fatal("failed validation deleted record")
		}
	}
}

func TestNewProjectTemplateDoesNotRequireTunnelNetwork(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	p, err := svc.Create("example", t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, compose, err := svc.GetComposeFile(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compose, "external:") || strings.Contains(compose, "cloudflared") {
		t.Fatalf("template forces a tunnel network: %s", compose)
	}
	if !strings.Contains(compose, "image: nginx:alpine") {
		t.Fatal("default service missing")
	}
}
