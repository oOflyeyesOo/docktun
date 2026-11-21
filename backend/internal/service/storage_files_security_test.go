package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestStorageBrowserConfinesProjectOperations(t *testing.T) {
	setup := setupFixture(t)
	directory, outside := t.TempDir(), t.TempDir()
	result, err := setup.db.Exec("INSERT INTO projects(name,path) VALUES(?,?)", "browser", directory)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	service := NewStorageFilesService(setup.db, setup.cfg)
	target := strconv.FormatInt(id, 10)
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"0", "-1", "missing", "99999999"} {
		if _, err := service.ListFiles("project", invalid, ""); err == nil {
			t.Fatalf("accepted missing project %q", invalid)
		}
		if _, _, _, err := service.GetDownloadStream("project", invalid, "file"); err == nil {
			t.Fatalf("download accepted missing project %q", invalid)
		}
	}
	for _, name := range []string{"../escape", "nested/../../escape", "..\\escape"} {
		if _, err := service.ListFiles("project", target, name); err == nil {
			t.Fatalf("listed traversal %q", name)
		}
		if _, err := service.ReadFile("project", target, name); err == nil {
			t.Fatalf("read traversal %q", name)
		}
		if err := service.WriteFile("project", target, name, "bad"); err == nil {
			t.Fatalf("wrote traversal %q", name)
		}
		if err := service.CreateItem("project", target, name, false); err == nil {
			t.Fatalf("created traversal %q", name)
		}
		if err := service.DeleteItem("project", target, name); err == nil {
			t.Fatalf("deleted traversal %q", name)
		}
	}
	if err := os.Symlink(outside, filepath.Join(directory, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := service.ListFiles("project", target, "escape"); err == nil {
		t.Fatal("listed outside root")
	}
	if _, err := service.ReadFile("project", target, "escape/secret"); err == nil {
		t.Fatal("read outside root")
	}
	if err := service.WriteFile("project", target, "escape/secret", "bad"); err == nil {
		t.Fatal("wrote outside root")
	}
	if err := service.CreateItem("project", target, "escape/created", false); err == nil {
		t.Fatal("created outside root")
	}
	if err := service.DeleteItem("project", target, "escape/secret"); err == nil {
		t.Fatal("deleted outside root")
	}
	got, err := os.ReadFile(filepath.Join(outside, "secret"))
	if err != nil || string(got) != "kept" {
		t.Fatal("outside content changed", err)
	}
}

func TestContainerBrowserTreatsShellSyntaxAsFilename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux shell fixture")
	}
	bin, directory := t.TempDir(), t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = exec ] || exit 1\nshift 2\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	service := NewStorageFilesService(nil, nil)
	name := filepath.Join(directory, "literal$(printf substituted)")
	if err := service.CreateItem("container", "fixture", name, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("filename was interpreted by shell: %v", err)
	}
	entries, err := service.ListFiles("container", "fixture", directory)
	if err != nil || len(entries) != 1 || entries[0].Name != filepath.Base(name) {
		t.Fatalf("listing changed filename: %#v %v", entries, err)
	}
	if err := service.DeleteItem("container", "fixture", name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatal("literal filename was not deleted", err)
	}
}
