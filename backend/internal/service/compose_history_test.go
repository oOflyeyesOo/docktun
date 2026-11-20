package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"docktun/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	return database
}

func TestComposeHistory_RecordAndList(t *testing.T) {
	database := setupTestDB(t)

	// Insert dummy project
	res, err := database.Exec(`INSERT INTO projects (name, path) VALUES ('test-stack', '/opt/projects/test-stack')`)
	if err != nil {
		t.Fatal(err)
	}
	projID, _ := res.LastInsertId()
	projectID := int(projID)

	// Record initial revision
	rev1, err := RecordComposeRevision(database, projectID, "docker-compose.yml", "services:\n  app:\n    image: nginx\n", "Initial import", 1)
	if err != nil {
		t.Fatalf("failed to record rev 1: %v", err)
	}
	if rev1.ID <= 0 || rev1.Filename != "docker-compose.yml" {
		t.Fatalf("invalid rev1: %+v", rev1)
	}

	// Record duplicate identical revision - should return same revision without inserting
	revDup, err := RecordComposeRevision(database, projectID, "docker-compose.yml", "services:\n  app:\n    image: nginx\n", "Duplicate", 1)
	if err != nil {
		t.Fatalf("failed on duplicate: %v", err)
	}
	if revDup.ID != rev1.ID {
		t.Fatalf("expected duplicate to return existing rev ID %d, got %d", rev1.ID, revDup.ID)
	}

	// Record updated revision
	rev2, err := RecordComposeRevision(database, projectID, "docker-compose.yml", "services:\n  app:\n    image: nginx:alpine\n", "Bumped to alpine", 1)
	if err != nil {
		t.Fatalf("failed to record rev 2: %v", err)
	}
	if rev2.ID <= rev1.ID {
		t.Fatalf("expected rev2 ID > rev1 ID, got rev2=%d rev1=%d", rev2.ID, rev1.ID)
	}

	// List revisions
	list, err := ListComposeRevisions(database, projectID)
	if err != nil {
		t.Fatalf("failed to list revisions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(list))
	}
	// Verify ordered DESC (most recent first)
	if list[0].ID != rev2.ID || list[1].ID != rev1.ID {
		t.Fatalf("expected list[0]=%d, list[1]=%d, got list[0]=%d, list[1]=%d", rev2.ID, rev1.ID, list[0].ID, list[1].ID)
	}

	// Get specific revision
	fetched, err := GetComposeRevision(database, projectID, rev1.ID)
	if err != nil {
		t.Fatalf("failed to get rev1: %v", err)
	}
	if !strings.Contains(fetched.Content, "image: nginx") {
		t.Fatalf("unexpected content in fetched rev1: %s", fetched.Content)
	}
}

func TestComposeHistory_Rollback(t *testing.T) {
	database := setupTestDB(t)
	tempDir := t.TempDir()
	projDir := filepath.Join(tempDir, "rollback-stack")
	_ = os.MkdirAll(projDir, 0755)

	res, err := database.Exec(`INSERT INTO projects (name, path) VALUES ('rollback-stack', ?)`, projDir)
	if err != nil {
		t.Fatal(err)
	}
	projID, _ := res.LastInsertId()
	projectID := int(projID)

	projectsSvc := NewProjects(database, tempDir)
	projectsSvc.requireMountInspection = func() bool { return false }

	// Save rev 1 via Projects service
	initialContent := "services:\n  web:\n    image: redis:6\n"
	if err := projectsSvc.SaveComposeFile(projectID, "docker-compose.yml", initialContent); err != nil {
		t.Fatalf("failed to save compose v1: %v", err)
	}

	// Save rev 2 via Projects service
	updatedContent := "services:\n  web:\n    image: redis:7\n"
	if err := projectsSvc.SaveComposeFile(projectID, "docker-compose.yml", updatedContent); err != nil {
		t.Fatalf("failed to save compose v2: %v", err)
	}

	revisions, err := projectsSvc.ListRevisions(projectID)
	if err != nil || len(revisions) < 2 {
		t.Fatalf("expected at least 2 revisions, got len=%d err=%v", len(revisions), err)
	}

	rev1ID := revisions[len(revisions)-1].ID

	// Rollback to rev 1
	rolledBack, err := projectsSvc.RollbackRevision(projectID, rev1ID, 1)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if !strings.Contains(rolledBack.Content, "redis:6") {
		t.Fatalf("expected rolled back content to contain redis:6, got %s", rolledBack.Content)
	}

	// Verify file on disk actually contains redis:6
	_, currentDiskContent, err := projectsSvc.GetComposeFile(projectID)
	if err != nil {
		t.Fatalf("failed to read disk compose: %v", err)
	}
	if !strings.Contains(currentDiskContent, "redis:6") {
		t.Fatalf("expected disk content to be rolled back to redis:6, got: %s", currentDiskContent)
	}
}
