package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"docktun/internal/config"
)

func TestStorageDoctor_DetectLXC(t *testing.T) {
	tempDir := t.TempDir()
	uidMapFile := filepath.Join(tempDir, "uid_map")
	cgroupFile := filepath.Join(tempDir, "cgroup")

	// 1. Standard host uid map (0 0 4294967295)
	if err := os.WriteFile(uidMapFile, []byte("         0          0 4294967295\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cgroupFile, []byte("0::/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	doc := NewStorageDoctor(nil, nil)
	doc.SetProcPaths(uidMapFile, cgroupFile)

	isLXC, _ := doc.DetectLXC()
	if isLXC {
		t.Fatalf("expected isLXC to be false for root mapping, got true")
	}

	// 2. Unprivileged LXC uid map (0 100000 65536)
	if err := os.WriteFile(uidMapFile, []byte("         0     100000      65536\n"), 0644); err != nil {
		t.Fatal(err)
	}
	isLXC, details := doc.DetectLXC()
	if !isLXC {
		t.Fatalf("expected isLXC to be true for 100000 mapping, got false")
	}
	if !strings.Contains(details, "100000") {
		t.Fatalf("expected details to contain 100000, got %s", details)
	}
}

func TestStorageDoctor_Diagnose_Healthy(t *testing.T) {
	tempDir := t.TempDir()
	dbDir := filepath.Join(tempDir, "data")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "app.db")
	_ = os.WriteFile(dbPath, []byte("sqlite data"), 0644)

	cfgDir := filepath.Join(tempDir, "cloudflared")
	_ = os.MkdirAll(cfgDir, 0755)
	cfgPath := filepath.Join(cfgDir, "config.yml")
	_ = os.WriteFile(cfgPath, []byte("tunnel: test"), 0644)

	cfg := &config.Config{
		DBPath:            dbPath,
		CloudflaredConfig: cfgPath,
	}

	doc := NewStorageDoctor(nil, cfg)
	doc.SetOwnershipProbe(func(fi os.FileInfo) (int, int, bool) {
		return 1000, 1000, true
	})

	report := doc.Diagnose(context.Background())
	if !report.Healthy {
		t.Fatalf("expected report to be healthy, got %+v", report)
	}
	if report.ErrorCount != 0 {
		t.Fatalf("expected 0 errors, got %d", report.ErrorCount)
	}
}

func TestStorageDoctor_Diagnose_NobodyOwnership(t *testing.T) {
	tempDir := t.TempDir()
	dbDir := filepath.Join(tempDir, "data")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "app.db")
	_ = os.WriteFile(dbPath, []byte("sqlite data"), 0644)

	cfg := &config.Config{
		DBPath: dbPath,
	}

	doc := NewStorageDoctor(nil, cfg)
	// Mock UID 65534 (nobody:nogroup)
	doc.SetOwnershipProbe(func(fi os.FileInfo) (int, int, bool) {
		return NobodyUID, NobodyGID, true
	})

	report := doc.Diagnose(context.Background())
	if report.WarningCount == 0 && report.ErrorCount == 0 {
		t.Fatalf("expected warnings or errors for UID 65534, got none")
	}

	hasRemediation := false
	for _, rem := range report.Remediations {
		if strings.Contains(rem.Command, "100000:100000") {
			hasRemediation = true
			break
		}
	}
	if !hasRemediation {
		t.Fatalf("expected LXC 100000:100000 remediation command, got %+v", report.Remediations)
	}
}

func TestExtractComposeBindMounts(t *testing.T) {
	composeYAML := `
services:
  web:
    image: nginx
    volumes:
      - /var/log/nginx:/var/log/nginx
      - ./data:/app/data
      - type: bind
        source: /opt/shared
        target: /shared
      - myvolume:/data
`
	mounts := extractComposeBindMounts([]byte(composeYAML), "/opt/projects/myapp")
	if len(mounts) != 3 {
		t.Fatalf("expected 3 bind mounts, got %d: %v", len(mounts), mounts)
	}

	expected := map[string]bool{
		"/var/log/nginx":                  true,
		"/opt/projects/myapp/data":        true,
		filepath.Clean("/opt/projects/myapp/data"): true,
		"/opt/shared":                     true,
	}

	for _, m := range mounts {
		cleanM := filepath.ToSlash(m)
		found := false
		for exp := range expected {
			if cleanM == filepath.ToSlash(exp) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected mount source: %s", m)
		}
	}
}
