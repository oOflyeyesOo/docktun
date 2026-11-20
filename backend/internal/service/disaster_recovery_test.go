package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"docktun/internal/config"
	"docktun/internal/db"
)

func TestDisasterRecovery_RoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "data", "app.db")
	database, err := db.New(dbPath)
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer database.Close()

	// Seed test data in database
	_, err = database.Exec(`CREATE TABLE test_data (id INTEGER PRIMARY KEY, note TEXT); INSERT INTO test_data(id, note) VALUES(1, 'hello disaster recovery');`)
	if err != nil {
		t.Fatalf("seed db: %v", err)
	}

	cfDir := filepath.Join(tempDir, "cloudflared")
	if err := os.MkdirAll(cfDir, 0755); err != nil {
		t.Fatalf("create cfDir: %v", err)
	}
	cfFile := filepath.Join(cfDir, "config.yml")
	if err := os.WriteFile(cfFile, []byte("tunnel: test-tunnel-id\ningress:\n  - service: http_status:404\n"), 0644); err != nil {
		t.Fatalf("write cf config: %v", err)
	}

	projDir := filepath.Join(tempDir, "projects")
	stackDir := filepath.Join(projDir, "my-stack")
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		t.Fatalf("create stackDir: %v", err)
	}
	composeFile := filepath.Join(stackDir, "docker-compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  web:\n    image: nginx:alpine\n"), 0644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	envFile := filepath.Join(stackDir, ".env")
	if err := os.WriteFile(envFile, []byte("FOO=BAR\n"), 0644); err != nil {
		t.Fatalf("write env: %v", err)
	}

	cfg := &config.Config{
		DBPath:            dbPath,
		CloudflaredConfig: cfFile,
	}

	svc := NewDisasterRecoveryService(database, cfg).WithPaths(dbPath, cfDir, projDir)

	// Export
	var buf bytes.Buffer
	if err := svc.ExportBackup(&buf); err != nil {
		t.Fatalf("ExportBackup failed: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatalf("ExportBackup generated empty archive")
	}

	// Prepare restore target locations
	restoreDir := t.TempDir()
	restoreDBPath := filepath.Join(restoreDir, "data", "app.db")
	restoreCFDir := filepath.Join(restoreDir, "cloudflared")
	restoreProjDir := filepath.Join(restoreDir, "projects")

	restoreSvc := NewDisasterRecoveryService(nil, nil).WithPaths(restoreDBPath, restoreCFDir, restoreProjDir)

	// Restore
	if err := restoreSvc.RestoreBackup(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	// Verify restored DB
	restoredDB, err := db.New(restoreDBPath)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer restoredDB.Close()

	var note string
	if err := restoredDB.QueryRow("SELECT note FROM test_data WHERE id=1").Scan(&note); err != nil {
		t.Fatalf("query restored db: %v", err)
	}
	if note != "hello disaster recovery" {
		t.Fatalf("restored note mismatch: got %q, want %q", note, "hello disaster recovery")
	}

	// Verify restored Cloudflared config
	restoredCFBytes, err := os.ReadFile(filepath.Join(restoreCFDir, "config.yml"))
	if err != nil {
		t.Fatalf("read restored cf config: %v", err)
	}
	if !strings.Contains(string(restoredCFBytes), "test-tunnel-id") {
		t.Fatalf("restored cf config content mismatch: %s", string(restoredCFBytes))
	}

	// Verify restored projects
	restoredComposeBytes, err := os.ReadFile(filepath.Join(restoreProjDir, "my-stack", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read restored compose: %v", err)
	}
	if !strings.Contains(string(restoredComposeBytes), "nginx:alpine") {
		t.Fatalf("restored compose content mismatch: %s", string(restoredComposeBytes))
	}

	restoredEnvBytes, err := os.ReadFile(filepath.Join(restoreProjDir, "my-stack", ".env"))
	if err != nil {
		t.Fatalf("read restored env: %v", err)
	}
	if !strings.Contains(string(restoredEnvBytes), "FOO=BAR") {
		t.Fatalf("restored env content mismatch: %s", string(restoredEnvBytes))
	}
}

func TestDisasterRecovery_SecurityValidation(t *testing.T) {
	tempDir := t.TempDir()
	targetDB := filepath.Join(tempDir, "app.db")
	targetCF := filepath.Join(tempDir, "cf")
	targetProj := filepath.Join(tempDir, "proj")
	svc := NewDisasterRecoveryService(nil, nil).WithPaths(targetDB, targetCF, targetProj)

	// Test 1: Path Traversal (Zip-Slip)
	t.Run("ZipSlipRejection", func(t *testing.T) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)

		payload := []byte("evil payload")
		tw.WriteHeader(&tar.Header{
			Name:     "../../../../evil.txt",
			Mode:     0644,
			Size:     int64(len(payload)),
			Typeflag: tar.TypeReg,
		})
		tw.Write(payload)
		tw.Close()
		gw.Close()

		err := svc.RestoreBackup(&buf)
		if err == nil {
			t.Fatal("expected RestoreBackup to reject path traversal archive, got nil")
		}
		if !strings.Contains(err.Error(), "traversal") && !strings.Contains(err.Error(), "escapes") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	// Test 2: Missing Database
	t.Run("MissingAppDb", func(t *testing.T) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)

		payload := []byte("just a config")
		tw.WriteHeader(&tar.Header{
			Name:     "cloudflared/config.yml",
			Mode:     0644,
			Size:     int64(len(payload)),
			Typeflag: tar.TypeReg,
		})
		tw.Write(payload)
		tw.Close()
		gw.Close()

		err := svc.RestoreBackup(&buf)
		if err == nil {
			t.Fatal("expected RestoreBackup to reject archive missing app.db, got nil")
		}
		if !strings.Contains(err.Error(), "app.db not found") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	// Test 3: Corrupted Database
	t.Run("CorruptedDatabase", func(t *testing.T) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)

		corruptDB := []byte("this is not a valid SQLite 3 database header")
		tw.WriteHeader(&tar.Header{
			Name:     "app.db",
			Mode:     0644,
			Size:     int64(len(corruptDB)),
			Typeflag: tar.TypeReg,
		})
		tw.Write(corruptDB)
		tw.Close()
		gw.Close()

		err := svc.RestoreBackup(&buf)
		if err == nil {
			t.Fatal("expected RestoreBackup to reject corrupt database, got nil")
		}
	})
}
