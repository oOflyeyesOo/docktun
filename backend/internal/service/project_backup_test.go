package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func backupArchive(t *testing.T, name string, mode int64, kind byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\necho restored\n")
	h := &tar.Header{Name: name, Mode: mode, Size: int64(len(content)), Typeflag: kind}
	if kind == tar.TypeSymlink {
		h.Size = 0
		h.Linkname = "../outside"
	}
	if e := tw.WriteHeader(h); e != nil {
		t.Fatal(e)
	}
	if kind == tar.TypeReg {
		tw.Write(content)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}
func TestRestoreAtomicAndConfined(t *testing.T) {
	fixture := setupFixture(t)
	svc := newNativeTestProjects(fixture.db, "")
	parent := t.TempDir()
	invalid := filepath.Join(parent, "invalid")
	if _, e := svc.Restore("invalid", invalid, bytes.NewReader([]byte("bad"))); e == nil {
		t.Fatal("accepted corrupt upload")
	}
	var n int
	fixture.db.QueryRow(`SELECT count(*) FROM projects WHERE name='invalid'`).Scan(&n)
	if n != 0 {
		t.Fatal("failed restore registered project")
	}
	if _, e := os.Stat(invalid); !os.IsNotExist(e) {
		t.Fatal("failed restore left directory")
	}
	existing := filepath.Join(parent, "existing")
	os.Mkdir(existing, 0700)
	outside := filepath.Join(parent, "outside")
	os.WriteFile(outside, []byte("keep"), 0600)
	os.Symlink(parent, filepath.Join(existing, "link"))
	if _, e := svc.Restore("existing", existing, bytes.NewReader(backupArchive(t, "files/link/outside", 0600, tar.TypeReg))); e == nil {
		t.Fatal("accepted existing destination")
	}
	if raw, _ := os.ReadFile(outside); string(raw) != "keep" {
		t.Fatal("overwrote outside file")
	}
	for _, name := range []string{"files/../outside", "files/link"} {
		kind := byte(tar.TypeReg)
		if name == "files/link" {
			kind = tar.TypeSymlink
		}
		if _, e := svc.Restore("unsafe", filepath.Join(parent, "unsafe"), bytes.NewReader(backupArchive(t, name, 0600, kind))); e == nil {
			t.Fatal("accepted unsafe entry")
		}
	}
	restored, e := svc.Restore("valid", filepath.Join(parent, "valid"), bytes.NewReader(backupArchive(t, "files/run.sh", 0755, tar.TypeReg)))
	if e != nil {
		t.Fatal(e)
	}
	if info, e := os.Stat(filepath.Join(restored.Path, "run.sh")); e != nil || info.Mode()&0100 == 0 {
		t.Fatal("lost executable bit")
	}
	source, e := svc.Create("source", filepath.Join(parent, "source"), "https://example.test/repo", "owner", "secret")
	if e != nil {
		t.Fatal(e)
	}
	os.Mkdir(source.Path, 0700)
	os.WriteFile(filepath.Join(source.Path, "custom.yml"), []byte("services: {}\n"), 0600)
	fixture.db.Exec(`INSERT INTO project_compose_sources(project_id,files,error) VALUES(?,?,?)`, source.ID, `["custom.yml"]`, "")
	var archive bytes.Buffer
	if _, e = svc.Backup(source.ID, &archive); e != nil {
		t.Fatal(e)
	}
	copy, e := svc.Restore("copy", filepath.Join(parent, "copy"), &archive)
	if e != nil {
		t.Fatal(e)
	}
	sources, e := storedComposeSources(fixture.db, copy.ID)
	if e != nil || len(sources) != 1 || sources[0] != "custom.yml" {
		t.Fatal("lost Compose source list")
	}
	if copy.RepoURL != source.RepoURL || copy.GitUsername != "owner" || copy.GitToken != "" {
		t.Fatal("metadata mismatch")
	}
}
