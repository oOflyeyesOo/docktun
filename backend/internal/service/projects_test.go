package service

import (
	"os"
	"path/filepath"
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

	projDir := filepath.Join(tmpDir, "my-app")
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
