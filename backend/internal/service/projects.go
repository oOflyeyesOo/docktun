package service

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"docktun/internal/db"
)

type Project struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	RepoURL     string `json:"repo_url"`
	GitUsername string `json:"git_username"`
	GitToken    string `json:"git_token,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type FileInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

type Projects struct {
	db       *db.DB
	selfPath string
	mu       sync.Mutex
	outMu    sync.RWMutex
	outputs  map[int]*projectOutput
}

type projectOutput struct {
	Lines []string
	Done  bool
}

func NewProjects(database *db.DB, selfPath string) *Projects {
	return &Projects{db: database, selfPath: selfPath, outputs: make(map[int]*projectOutput)}
}

func (s *Projects) GetOutput(id int) ([]string, bool) {
	s.outMu.RLock()
	defer s.outMu.RUnlock()
	o, ok := s.outputs[id]
	if !ok {
		return nil, true
	}
	lines := make([]string, len(o.Lines))
	copy(lines, o.Lines)
	return lines, o.Done
}

func (s *Projects) clearOutput(id int) {
	s.outMu.Lock()
	s.outputs[id] = &projectOutput{}
	s.outMu.Unlock()
}

func (s *Projects) appendOutput(id int, line string) {
	s.outMu.Lock()
	if o, ok := s.outputs[id]; ok {
		o.Lines = append(o.Lines, line)
	}
	s.outMu.Unlock()
}

func (s *Projects) markDone(id int) {
	s.outMu.Lock()
	if o, ok := s.outputs[id]; ok {
		o.Done = true
	}
	s.outMu.Unlock()
}

func (s *Projects) List() ([]Project, error) {
	// Auto-prune ghost stacks and duplicate container-name entries
	_, _ = s.db.Exec(`DELETE FROM projects WHERE name = 'public-cloudflare-tunnel-cloudflared' OR name LIKE '%cloudflare-tunnel%'`)
	_, _ = s.db.Exec(`DELETE FROM projects WHERE name IN ('docktun-cloudflared', 'docktun-wireguard', 'docktun-tailscale', 'docktun-zeroconf')`)
	_, _ = s.db.Exec(`DELETE FROM projects WHERE name = 'mediastorm-postgres' AND EXISTS (SELECT 1 FROM projects p2 WHERE p2.name = 'mediastorm')`)
	_, _ = s.db.Exec(`DELETE FROM projects WHERE name = 'tun' AND EXISTS (SELECT 1 FROM projects p2 WHERE p2.name = 'docktun')`)

	rows, err := s.db.Query("SELECT id, name, path, repo_url, git_username, git_token, created_at FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.RepoURL, &p.GitUsername, &p.GitToken, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.GitToken = maskToken(p.GitToken)
		projects = append(projects, p)
	}
	return projects, nil
}

func (s *Projects) PruneOrphans() (int, error) {
	res, err := s.db.Exec(`
		DELETE FROM projects 
		WHERE name IN ('public-cloudflare-tunnel-cloudflared', 'docktun-cloudflared', 'docktun-wireguard', 'docktun-tailscale', 'docktun-zeroconf')
		   OR name LIKE '%cloudflare-tunnel%'
		   OR (name = 'mediastorm-postgres' AND EXISTS (SELECT 1 FROM projects p2 WHERE p2.name = 'mediastorm'))
		   OR (name = 'tun' AND EXISTS (SELECT 1 FROM projects p2 WHERE p2.name = 'docktun'))
	`)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

func (s *Projects) Create(name, path, repoURL, gitUsername, gitToken string) (Project, error) {
	if s.isSelf(path) {
		return Project{}, fmt.Errorf("cannot add DockTun itself as a project")
	}
	res, err := s.db.Exec("INSERT INTO projects (name, path, repo_url, git_username, git_token) VALUES (?, ?, ?, ?, ?)",
		name, path, repoURL, gitUsername, gitToken)
	if err != nil {
		return Project{}, err
	}
	id, _ := res.LastInsertId()
	return Project{ID: int(id), Name: name, Path: path, RepoURL: repoURL, GitUsername: gitUsername}, nil
}

func (s *Projects) Update(id int, name, path, repoURL, gitUsername, gitToken string) error {
	if s.isSelf(path) {
		return fmt.Errorf("cannot set path to DockTun itself")
	}
	if gitToken == "" || gitToken == maskToken("x") {
		_, err := s.db.Exec("UPDATE projects SET name=?, path=?, repo_url=?, git_username=? WHERE id=?",
			name, path, repoURL, gitUsername, id)
		return err
	}
	_, err := s.db.Exec("UPDATE projects SET name=?, path=?, repo_url=?, git_username=?, git_token=? WHERE id=?",
		name, path, repoURL, gitUsername, gitToken, id)
	return err
}

func (s *Projects) isSelf(path string) bool {
	if s.selfPath == "" {
		return false
	}
	return strings.TrimRight(path, "/") == strings.TrimRight(s.selfPath, "/")
}

func (s *Projects) Delete(id int) error {
	_, err := s.db.Exec("DELETE FROM projects WHERE id = ?", id)
	return err
}

func (s *Projects) GetByID(id int) (Project, error) {
	var p Project
	err := s.db.QueryRow("SELECT id, name, path, repo_url, git_username, git_token, created_at FROM projects WHERE id = ?", id).
		Scan(&p.ID, &p.Name, &p.Path, &p.RepoURL, &p.GitUsername, &p.GitToken, &p.CreatedAt)
	return p, err
}

func (s *Projects) ComposeAction(id int, path string, onDone func(string), args ...string) {
	s.clearOutput(id)
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.runComposeToBuffer(id, path, args...)
		s.markDone(id)
		if onDone != nil {
			lines, _ := s.GetOutput(id)
			onDone(strings.Join(lines, "\n"))
		}
	}()
}

func (s *Projects) DeployStream(id int, p Project, ref string, onDone func(string)) {
	s.clearOutput(id)
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		if p.RepoURL == "" && p.GitToken == "" {
			s.appendOutput(id, "[STEP] Pulling latest images...")
			s.runComposeToBuffer(id, p.Path, "pull")
			s.appendOutput(id, "[STEP] Starting containers...")
			s.runComposeToBuffer(id, p.Path, "up", "-d")
			s.markDone(id)
			if onDone != nil {
				lines, _ := s.GetOutput(id)
				onDone(strings.Join(lines, "\n"))
			}
			return
		}

		remoteURL := s.buildAuthURL(p)
		if remoteURL != "" {
			cmd := exec.Command("git", "remote", "set-url", "origin", remoteURL)
			cmd.Dir = p.Path
			cmd.CombinedOutput()
		}

		s.appendOutput(id, "[STEP] git fetch --all")
		if out, err := s.runGit(p.Path, "fetch", "--all"); err != nil {
			s.appendOutput(id, "[ERROR] git fetch failed: "+out)
			s.markDone(id)
			if onDone != nil {
				lines, _ := s.GetOutput(id)
				onDone(strings.Join(lines, "\n"))
			}
			return
		}

		s.appendOutput(id, "[STEP] git checkout "+ref)
		if out, err := s.runGit(p.Path, "checkout", ref); err != nil {
			s.appendOutput(id, "[ERROR] git checkout failed: "+out)
			s.markDone(id)
			if onDone != nil {
				lines, _ := s.GetOutput(id)
				onDone(strings.Join(lines, "\n"))
			}
			return
		}

		s.appendOutput(id, "[STEP] git pull origin "+ref)
		if out, err := s.runGit(p.Path, "pull", "origin", ref); err != nil {
			s.appendOutput(id, "[ERROR] git pull failed: "+out)
			s.markDone(id)
			if onDone != nil {
				lines, _ := s.GetOutput(id)
				onDone(strings.Join(lines, "\n"))
			}
			return
		}

		s.appendOutput(id, "[STEP] docker compose up -d --build")
		s.runComposeToBuffer(id, p.Path, "up", "-d", "--build")
		s.markDone(id)
		if onDone != nil {
			lines, _ := s.GetOutput(id)
			onDone(strings.Join(lines, "\n"))
		}
	}()
}

func (s *Projects) runComposeToBuffer(id int, path string, args ...string) {
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		f := filepath.Join(path, name)
		if fileExists(f) {
			SanitizeComposeFile(f)
			break
		}
	}

	cmdArgs := []string{"compose"}
	if p, err := s.GetByID(id); err == nil && p.Name != "" {
		cmdArgs = append(cmdArgs, "-p", p.Name)
	}
	cmdArgs = append(cmdArgs, "--project-directory", path)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)

	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		s.appendOutput(id, "[ERROR] "+err.Error())
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			s.appendOutput(id, strings.TrimSpace(string(buf[:n])))
		}
		if err != nil {
			break
		}
	}
	cmd.Wait()
}

func (s *Projects) runGit(path string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (s *Projects) buildAuthURL(p Project) string {
	if p.RepoURL == "" || p.GitToken == "" {
		return ""
	}
	u, err := url.Parse(p.RepoURL)
	if err != nil {
		return ""
	}
	username := p.GitUsername
	if username == "" {
		username = "oauth2"
	}
	u.User = url.UserPassword(username, p.GitToken)
	return u.String()
}

func maskToken(t string) string {
	if len(t) <= 4 {
		return "****"
	}
	return "****" + t[len(t)-4:]
}

func (s *Projects) resolvePath(p Project, relPath string) (string, error) {
	cleanBase := filepath.Clean(p.Path)
	cleanRel := filepath.Clean("/" + filepath.ToSlash(relPath))
	fullPath := filepath.Clean(filepath.Join(cleanBase, cleanRel))
	rel, err := filepath.Rel(cleanBase, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid path: outside project root")
	}
	return fullPath, nil
}

func (s *Projects) ListFiles(id int, relPath string) ([]FileInfo, error) {
	p, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	targetDir, err := s.resolvePath(p, relPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil, err
	}

	cleanBase := filepath.Clean(p.Path)
	var files []FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		entryFullPath := filepath.Join(targetDir, entry.Name())
		relEntry, _ := filepath.Rel(cleanBase, entryFullPath)
		files = append(files, FileInfo{
			Name:    entry.Name(),
			Path:    filepath.ToSlash(relEntry),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	if files == nil {
		files = []FileInfo{}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	return files, nil
}

func (s *Projects) GetFile(id int, relPath string) (string, error) {
	p, err := s.GetByID(id)
	if err != nil {
		return "", err
	}
	targetFile, err := s.resolvePath(p, relPath)
	if err != nil {
		return "", err
	}
	stat, err := os.Stat(targetFile)
	if err != nil {
		return "", err
	}
	if stat.IsDir() {
		return "", fmt.Errorf("path is a directory, not a file")
	}
	if stat.Size() > 5*1024*1024 {
		return "", fmt.Errorf("file is too large (> 5MB)")
	}
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Projects) SaveFile(id int, relPath string, content string) error {
	p, err := s.GetByID(id)
	if err != nil {
		return err
	}
	targetFile, err := s.resolvePath(p, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetFile), 0755); err != nil {
		return err
	}
	return os.WriteFile(targetFile, []byte(content), 0644)
}

func (s *Projects) DeleteFile(id int, relPath string) error {
	p, err := s.GetByID(id)
	if err != nil {
		return err
	}
	targetFile, err := s.resolvePath(p, relPath)
	if err != nil {
		return err
	}
	cleanBase := filepath.Clean(p.Path)
	if targetFile == cleanBase {
		return fmt.Errorf("cannot delete project root directory")
	}
	return os.RemoveAll(targetFile)
}

func (s *Projects) CreateFile(id int, relPath string, isDir bool, content string) error {
	p, err := s.GetByID(id)
	if err != nil {
		return err
	}
	targetPath, err := s.resolvePath(p, relPath)
	if err != nil {
		return err
	}
	if isDir {
		return os.MkdirAll(targetPath, 0755)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(targetPath, []byte(content), 0644)
}

func (s *Projects) GetComposeFile(id int) (string, string, error) {
	p, err := s.GetByID(id)
	if err != nil {
		return "", "", err
	}
	candidates := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
	for _, c := range candidates {
		fullPath := filepath.Join(p.Path, c)
		if data, err := os.ReadFile(fullPath); err == nil {
			return c, string(data), nil
		}
	}
	defaultTmpl := fmt.Sprintf(`services:
  %s:
    image: nginx:alpine
    container_name: %s
    restart: unless-stopped
    ports:
      - "8080:80"
    networks:
      - cloudflared

networks:
  cloudflared:
    external: true
`, p.Name, p.Name)
	return "docker-compose.yml", defaultTmpl, nil
}

func (s *Projects) SaveComposeFile(id int, filename string, content string) error {
	if filename == "" {
		filename = "docker-compose.yml"
	}
	return s.SaveFile(id, filename, content)
}

func (s *Projects) GetEnvFile(id int) (string, error) {
	content, err := s.GetFile(id, ".env")
	if err != nil {
		return "", nil
	}
	return content, nil
}

func (s *Projects) SaveEnvFile(id int, content string) error {
	return s.SaveFile(id, ".env", content)
}

