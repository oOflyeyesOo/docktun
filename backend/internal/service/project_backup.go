package service

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type projectBackupManifest struct {
	Version        int      `json:"version"`
	Project        Project  `json:"project"`
	ComposeSources []string `json:"compose_sources,omitempty"`
}

func (s *Projects) Backup(id int, out io.Writer) (string, error) {
	project, err := s.GetByID(id)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(project.Path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	sources, err := storedComposeSources(s.db, id)
	if err != nil {
		return "", err
	}
	project.GitToken = ""
	if u, e := url.Parse(project.RepoURL); e == nil {
		u.User = nil
		project.RepoURL = u.String()
	}
	manifest, _ := json.Marshal(projectBackupManifest{Version: 1, Project: project, ComposeSources: sources})
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	defer gz.Close()
	defer tw.Close()
	if err = writeBackupFile(tw, "manifest.json", manifest); err != nil {
		return "", err
	}
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return fs.SkipDir
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("backup contains a non-regular file: %s", name)
		}
		if total+info.Size() > projectBackupLimit && !info.IsDir() {
			return fmt.Errorf("project backup exceeds 512 MiB")
		}
		header, e := tar.FileInfoHeader(info, "")
		if e != nil {
			return e
		}
		header.Name = "files/" + name
		if e = tw.WriteHeader(header); e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		file, e := root.Open(name)
		if e != nil {
			return e
		}
		_, copyErr := io.CopyN(tw, file, info.Size())
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return "", err
	}
	if err = tw.Close(); err != nil {
		return "", err
	}
	if err = gz.Close(); err != nil {
		return "", err
	}
	return project.Name, nil
}
func writeBackupFile(tw *tar.Writer, name string, content []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(content))}); err != nil {
		return err
	}
	_, err := tw.Write(content)
	return err
}
func (s *Projects) Restore(name, destination string, input io.Reader) (*Project, error) {
	name = strings.TrimSpace(name)
	destination = strings.TrimSpace(destination)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || !filepath.IsAbs(destination) {
		return nil, fmt.Errorf("provide a safe project name and absolute path")
	}
	destination = filepath.Clean(destination)
	if s.isSelf(destination) {
		return nil, fmt.Errorf("cannot restore over DockTun")
	}
	// Registration and an existing folder must never be treated as an overwrite request.
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return nil, fmt.Errorf("restore destination must not exist")
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0750); err != nil {
		return nil, err
	}
	physical, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, err
	}
	if physical != parent {
		return nil, fmt.Errorf("restore parent must not contain symbolic links")
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	defer parentRoot.Close()
	staging, err := os.MkdirTemp(parent, ".docktun-restore-")
	if err != nil {
		return nil, err
	}
	defer parentRoot.RemoveAll(filepath.Base(staging))
	root, err := os.OpenRoot(staging)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	gz, err := gzip.NewReader(input)
	if err != nil {
		return nil, fmt.Errorf("invalid project backup: %w", err)
	}
	defer gz.Close()
	// Cap decompressed bytes including metadata and archive headers, not just files.
	limited := &io.LimitedReader{R: gz, N: projectBackupLimit + (16 << 20)}
	tr := tar.NewReader(limited)
	manifest := projectBackupManifest{}
	var total int64
	files := 0
	seen := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("invalid project backup: %w", e)
		}
		if h.Name == "manifest.json" {
			if manifest.Version != 0 || h.Typeflag != tar.TypeReg || h.Size > 1<<20 {
				return nil, fmt.Errorf("invalid backup manifest")
			}
			raw, e := io.ReadAll(io.LimitReader(tr, 1<<20))
			if e != nil {
				return nil, e
			}
			if e = json.Unmarshal(raw, &manifest); e != nil || manifest.Version != 1 {
				return nil, fmt.Errorf("unsupported backup manifest")
			}
			continue
		}
		if h.Name == "project.json" {
			continue
		} // Original file-only format remains importable.
		if !strings.HasPrefix(h.Name, "files/") {
			return nil, fmt.Errorf("unexpected archive entry")
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(h.Name, "files/"), "/")
		if rel == "" || !fs.ValidPath(rel) || strings.Contains(rel, "\\") || path.Clean(rel) != rel || seen[rel] {
			return nil, fmt.Errorf("unsafe or duplicate archive path")
		}
		seen[rel] = true
		if h.Typeflag == tar.TypeDir {
			if e := root.MkdirAll(rel, 0750); e != nil {
				return nil, e
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 || total+h.Size > projectBackupLimit {
			return nil, fmt.Errorf("unsupported or oversized archive entry")
		}
		if e := root.MkdirAll(path.Dir(rel), 0750); e != nil {
			return nil, e
		}
		mode := os.FileMode(0600)
		if h.Mode&0111 != 0 {
			mode = 0700
		}
		file, e := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if e != nil {
			return nil, e
		}
		_, e = io.CopyN(file, tr, h.Size)
		closeErr := file.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		total += h.Size
		files++
	}
	// Consume gzip trailers so corruption/truncation cannot be reported as success.
	if _, err = io.Copy(io.Discard, limited); err != nil {
		return nil, fmt.Errorf("invalid gzip trailer: %w", err)
	}
	if limited.N == 0 || files == 0 {
		return nil, fmt.Errorf("empty or oversized project backup")
	}
	for _, source := range manifest.ComposeSources {
		if !fs.ValidPath(source) {
			return nil, fmt.Errorf("unsafe Compose source")
		}
		info, e := root.Stat(source)
		if e != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Compose source is missing")
		}
	}
	// Hold project mutation lock through final registration. Publishing by rename is
	// local to one filesystem; no old data is ever truncated.
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM projects WHERE path=? OR lower(name)=lower(?)`, destination, name).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, ErrProjectConflict
	}
	if _, err = parentRoot.Lstat(filepath.Base(destination)); !os.IsNotExist(err) {
		return nil, fmt.Errorf("restore destination already exists")
	}
	if err = parentRoot.Rename(filepath.Base(staging), filepath.Base(destination)); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			parentRoot.RemoveAll(filepath.Base(destination))
		}
	}()
	result, err := tx.Exec(`INSERT INTO projects(name,path,repo_url,git_username,git_token) VALUES(?,?,?,?,?)`, name, destination, manifest.Project.RepoURL, manifest.Project.GitUsername, "")
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if len(manifest.ComposeSources) > 0 {
		raw, _ := json.Marshal(manifest.ComposeSources)
		if _, err = tx.Exec(`INSERT INTO project_compose_sources(project_id,files,error) VALUES(?,?,?)`, id, string(raw), ""); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &Project{ID: int(id), Name: name, Path: destination, RepoURL: manifest.Project.RepoURL, GitUsername: manifest.Project.GitUsername}, nil
}
