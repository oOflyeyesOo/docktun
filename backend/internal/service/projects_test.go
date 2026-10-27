package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestComposeActionsResolveLegacyRelativePathsOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture targets the Linux production runtime")
	}
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("projects/example", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("projects/example/compose.yml", []byte("services:\n  example:\n    image: alpine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--project-directory" ]; then
  shift
  if [ ! -f "$1/compose.yml" ]; then echo "project directory resolved twice"; exit 1; fi
  if [ "$(pwd)" != "$1" ]; then echo "working directory mismatch"; exit 1; fi
  case "$*" in
   *config*) echo '{"services":{"example":{"image":"alpine"}}}' ;;
   *) echo COMPOSE_PATH_OK ;;
  esac
  exit 0
 fi
 shift
done
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	database, err := db.New(filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	project, err := svc.Create("example", "projects/example", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(project.Path) {
		t.Fatal("new project path is not absolute")
	}
	svc.clearOutput(project.ID)
	svc.runComposeToBuffer(project.ID, "projects/example", "up", "-d")
	lines, _ := svc.GetOutput(project.ID)
	output := strings.Join(lines, "\n")
	if strings.Contains(output, "[ERROR]") || !strings.Contains(output, "COMPOSE_PATH_OK") {
		t.Fatalf("relative stack action failed: %s", output)
	}
}

func TestListingProjectsNeverDeletesByName(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	names := []string{"public-cloudflare-tunnel-cloudflared", "personal-cloudflare-tunnel", "docktun-wireguard", "mediastorm", "mediastorm-postgres", "tun", "docktun"}
	ids := make(map[int]bool)
	for _, name := range names {
		project, err := svc.Create(name, filepath.Join(t.TempDir(), name), "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		ids[project.ID] = true
	}
	for attempt := 0; attempt < 2; attempt++ {
		projects, err := svc.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != len(names) {
			t.Fatalf("listing removed user projects: %v", projects)
		}
		for _, project := range projects {
			if !ids[project.ID] {
				t.Fatal("listing changed project identity")
			}
		}
	}
	if count, err := svc.PruneDuplicates(); err != nil || count != 0 {
		t.Fatalf("cleanup guessed orphaned projects: %d %v", count, err)
	}
}

func TestProjectCleanupKeepsDistinctCredentialsAndOnlyRemovesExactDuplicates(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	original, err := svc.Create("example", t.TempDir(), "https://example.invalid/project", "user", "token-one")
	if err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO projects(name,path,repo_url,git_username,git_token) SELECT name,path,repo_url,git_username,git_token FROM projects WHERE id=?`
	if _, err := database.Exec(insert, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO projects(name,path,repo_url,git_username,git_token) SELECT name,path,repo_url,git_username,'token-two' FROM projects WHERE id=?`, original.ID); err != nil {
		t.Fatal(err)
	}
	before, err := svc.List()
	if err != nil || len(before) != 3 {
		t.Fatal("listing changed duplicate records before explicit cleanup")
	}
	count, err := svc.PruneDuplicates()
	if err != nil || count != 1 {
		t.Fatalf("cleanup removed distinct metadata: %d %v", count, err)
	}
	after, err := svc.List()
	if err != nil || len(after) != 2 {
		t.Fatal("distinct project record lost")
	}
	if _, err := svc.GetByID(original.ID); err != nil {
		t.Fatal("original identity removed")
	}
}

func TestMissingProjectFilesDoNotCreateDirectoriesOrInventCompose(t *testing.T) {
	root := t.TempDir()
	database, err := db.New(filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	missing := filepath.Join(root, "host-only", "example")
	project, err := svc.Create("example", missing, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListFiles(project.ID, ""); err == nil {
		t.Fatal("unmounted folder shown as empty")
	}
	if _, _, err := svc.GetComposeFile(project.ID); err == nil {
		t.Fatal("unmounted stack replaced with sample nginx Compose")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("read operation created a shadow host directory")
	}
}

func TestUnreadableComposeIsNotReplacedBySample(t *testing.T) {
	root := t.TempDir()
	database, err := db.New(filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	directory := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(directory, "docker-compose.yml"), 0755); err != nil {
		t.Fatal(err)
	}
	project, err := svc.Create("example", directory, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetComposeFile(project.ID); err == nil {
		t.Fatal("invalid/unreadable Compose source silently replaced")
	}
}

func TestEnvReadDistinguishesAbsentFileFromUnavailableProject(t *testing.T) {
	root := t.TempDir()
	database, err := db.New(filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	project, err := svc.Create("example", filepath.Join(root, "missing"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetEnvFile(project.ID); err == nil {
		t.Fatal("unmounted project reported empty env")
	}
	if err := os.MkdirAll(project.Path, 0755); err != nil {
		t.Fatal(err)
	}
	if content, err := svc.GetEnvFile(project.ID); err != nil || content != "" {
		t.Fatalf("optional env: %q %v", content, err)
	}
	if err := os.Mkdir(filepath.Join(project.Path, ".env"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetEnvFile(project.ID); err == nil {
		t.Fatal("invalid env reported empty")
	}
}

func TestProjectFilesConfineSymlinksAndTraversal(t *testing.T) {
	root := t.TempDir()
	database, err := db.New(filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	directory := filepath.Join(root, "project")
	outside := filepath.Join(root, "outside")
	for _, path := range []string{directory, outside} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}
	project, err := svc.Create("example", directory, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetFile(project.ID, "escape/secret"); err == nil {
		t.Fatal("read escaped project")
	}
	if _, err := svc.ListFiles(project.ID, "escape"); err == nil {
		t.Fatal("list escaped project")
	}
	if err := svc.SaveFile(project.ID, "escape/secret", "overwritten"); err == nil {
		t.Fatal("write escaped project")
	}
	if err := svc.CreateFile(project.ID, "escape/new-dir", true, ""); err == nil {
		t.Fatal("mkdir escaped project")
	}
	if err := svc.DeleteFile(project.ID, "escape/secret"); err == nil {
		t.Fatal("delete escaped project")
	}
	if err := svc.SaveFile(project.ID, "../outside/secret", "overwritten"); err == nil {
		t.Fatal("accepted parent traversal")
	}
	if err := svc.DeleteFile(project.ID, ""); err == nil {
		t.Fatal("deleted project root")
	}
	if content, err := os.ReadFile(secret); err != nil || string(content) != "preserve" {
		t.Fatal("outside file changed")
	}
	if err := svc.SaveFile(project.ID, "nested/file.txt", "inside"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nested", filepath.Join(directory, "inside-link")); err != nil {
		t.Fatal(err)
	}
	if content, err := svc.GetFile(project.ID, "inside-link/file.txt"); err != nil || content != "inside" {
		t.Fatalf("safe relative symlink: %q %v", content, err)
	}
	if err := svc.SaveFile(project.ID, "inside-link/file.txt", "updated"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(directory, "compose.yml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetComposeFile(project.ID); err == nil {
		t.Fatal("Compose read escaped through symlink")
	}
	if err := svc.DeleteFile(project.ID, "escape"); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(secret); err != nil || string(content) != "preserve" {
		t.Fatal("deleting symlink removed target")
	}
	if err := svc.DeleteFile(project.ID, "nested"); err != nil {
		t.Fatal(err)
	}
}

func TestProjectSettingsPreserveMaskedGitCredentials(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc := NewProjects(database, "")
	project, err := svc.Create("example", t.TempDir(), "https://example.invalid/private", "owner", "original-secret-1234")
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.List()
	if err != nil || len(list) != 1 {
		t.Fatal("missing project")
	}
	if list[0].GitToken != "****1234" {
		t.Fatal("expected masked API credential")
	}
	if err := svc.Update(project.ID, "renamed", project.Path, project.RepoURL, "new-user", list[0].GitToken); err != nil {
		t.Fatal(err)
	}
	saved, err := svc.GetByID(project.ID)
	if err != nil || saved.GitToken != "original-secret-1234" || saved.Name != "renamed" || saved.GitUsername != "new-user" {
		t.Fatal("settings save corrupted credential or metadata")
	}
	if err := svc.Update(project.ID, saved.Name, saved.Path, saved.RepoURL, saved.GitUsername, "replacement-secret-5678"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Update(project.ID, "stale name", saved.Path, saved.RepoURL, saved.GitUsername, list[0].GitToken); err == nil {
		t.Fatal("stale form overwrote replacement credential")
	}
	saved, err = svc.GetByID(project.ID)
	if err != nil || saved.GitToken != "replacement-secret-5678" || saved.Name != "renamed" {
		t.Fatal("stale save mutated project")
	}
	for _, unchanged := range []string{"", "****", "****5678"} {
		if err := svc.Update(project.ID, saved.Name, saved.Path, saved.RepoURL, saved.GitUsername, unchanged); err != nil {
			t.Fatal(err)
		}
		current, err := svc.GetByID(project.ID)
		if err != nil || current.GitToken != "replacement-secret-5678" {
			t.Fatal("unchanged credential replaced")
		}
	}
}
