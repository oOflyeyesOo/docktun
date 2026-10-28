package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	db        *db.DB
	selfPath  string
	mu        sync.Mutex
	outMu     sync.RWMutex
	outputs   map[int]*projectOutput
	runDocker func(context.Context, ...string) ([]byte, error)
}

type projectOutput struct {
	Lines []string
	Done  bool
}

func NewProjects(database *db.DB, selfPath string) *Projects {
	return &Projects{db: database, selfPath: selfPath, outputs: make(map[int]*projectOutput), runDocker: providerDocker}
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
	return projects, rows.Err()
}

// Cleanup is explicit and only removes records with identical project metadata.
// A provider-like name or an inaccessible host path does not make a stack orphaned.
func (s *Projects) PruneDuplicates() (int, error) {
	result, err := s.db.Exec(`DELETE FROM projects WHERE id NOT IN (
  SELECT MIN(id) FROM projects GROUP BY name,path,repo_url,git_username,git_token
 )`)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

// ProjectsRoot uses the persistent mount in the distributed image, and an
// absolute local folder when running the backend directly during development.
func ProjectsRoot() string {
	if info, err := os.Stat("/opt/projects"); err == nil && info.IsDir() {
		return "/opt/projects"
	}
	root, _ := filepath.Abs("./projects")
	return root
}

func (s *Projects) Create(name, path, repoURL, gitUsername, gitToken string) (Project, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Project{}, err
	}
	path = absolute
	if s.isSelf(path) {
		return Project{}, fmt.Errorf("cannot add DockTun itself as a project")
	}

	// Check if a project with the same path or name already exists to prevent UNIQUE constraint errors
	var existing Project
	err = s.db.QueryRow("SELECT id, name, path, repo_url, git_username, git_token, created_at FROM projects WHERE path = ? OR name = ?", path, name).
		Scan(&existing.ID, &existing.Name, &existing.Path, &existing.RepoURL, &existing.GitUsername, &existing.GitToken, &existing.CreatedAt)
	if err == nil {
		// Update existing record
		_, updateErr := s.db.Exec("UPDATE projects SET name = ?, path = ?, repo_url = ?, git_username = ?, git_token = ? WHERE id = ?",
			name, path, repoURL, gitUsername, gitToken, existing.ID)
		if updateErr != nil {
			return Project{}, updateErr
		}
		return Project{
			ID:          existing.ID,
			Name:        name,
			Path:        path,
			RepoURL:     repoURL,
			GitUsername: gitUsername,
			CreatedAt:   existing.CreatedAt,
		}, nil
	}

	res, err := s.db.Exec("INSERT INTO projects (name, path, repo_url, git_username, git_token) VALUES (?, ?, ?, ?, ?)",
		name, path, repoURL, gitUsername, gitToken)
	if err != nil {
		// Fallback in case of concurrent insert or edge constraint
		err2 := s.db.QueryRow("SELECT id, name, path, repo_url, git_username, git_token, created_at FROM projects WHERE path = ? OR name = ?", path, name).
			Scan(&existing.ID, &existing.Name, &existing.Path, &existing.RepoURL, &existing.GitUsername, &existing.GitToken, &existing.CreatedAt)
		if err2 == nil {
			_ = s.Update(existing.ID, name, path, repoURL, gitUsername, gitToken)
			return Project{
				ID:          existing.ID,
				Name:        name,
				Path:        path,
				RepoURL:     repoURL,
				GitUsername: gitUsername,
				CreatedAt:   existing.CreatedAt,
			}, nil
		}
		return Project{}, err
	}
	id, _ := res.LastInsertId()
	return Project{ID: int(id), Name: name, Path: path, RepoURL: repoURL, GitUsername: gitUsername}, nil
}

func (s *Projects) GetByPath(path string) (*Project, error) {
	var p Project
	err := s.db.QueryRow("SELECT id, name, path, repo_url, git_username, git_token, created_at FROM projects WHERE path = ?", path).
		Scan(&p.ID, &p.Name, &p.Path, &p.RepoURL, &p.GitUsername, &p.GitToken, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.GitToken = maskToken(p.GitToken)
	return &p, nil
}

func (s *Projects) GetByName(name string) (*Project, error) {
	var p Project
	err := s.db.QueryRow("SELECT id, name, path, repo_url, git_username, git_token, created_at FROM projects WHERE name = ?", name).
		Scan(&p.ID, &p.Name, &p.Path, &p.RepoURL, &p.GitUsername, &p.GitToken, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.GitToken = maskToken(p.GitToken)
	return &p, nil
}

func (s *Projects) Update(id int, name, path, repoURL, gitUsername, gitToken string) error {
	if s.isSelf(path) {
		return fmt.Errorf("cannot set path to DockTun itself")
	}
	current, err := s.GetByID(id)
	if err != nil {
		return err
	}
	if strings.HasPrefix(gitToken, "****") && gitToken != "****" && gitToken != maskToken(current.GitToken) {
		return fmt.Errorf("saved Git token has changed; reload the project settings before saving")
	}
	if gitToken == "" || gitToken == "****" || gitToken == maskToken(current.GitToken) {
		_, err := s.db.Exec("UPDATE projects SET name=?, path=?, repo_url=?, git_username=? WHERE id=?",
			name, path, repoURL, gitUsername, id)
		return err
	}
	_, err = s.db.Exec("UPDATE projects SET name=?, path=?, repo_url=?, git_username=?, git_token=? WHERE id=?",
		name, path, repoURL, gitUsername, gitToken, id)
	return err
}

func (s *Projects) isSelf(path string) bool {
	if s.selfPath == "" {
		return false
	}
	return strings.TrimRight(path, "/") == strings.TrimRight(s.selfPath, "/")
}

// DeleteProject removes stack resources by their exact Compose project label.
// Compose working_dir labels refer to the daemon host, not necessarily a path
// accessible inside DockTun. Never depend on that path to remove resources.
func (s *Projects) DeleteProject(id int, down bool, removeFiles bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.GetByID(id)
	if err != nil {
		return err
	}
	if s.isSelf(p.Path) {
		return fmt.Errorf("cannot delete DockTun itself as a project")
	}
	if removeFiles {
		if err := s.validateProjectRemoval(p.Path); err != nil {
			return err
		}
	}
	if down {
		if err := s.removeStackResources(p.Name); err != nil {
			return err
		}
	}
	if removeFiles {
		if err := os.RemoveAll(filepath.Clean(p.Path)); err != nil {
			return fmt.Errorf("containers removed but project files could not be deleted: %w", err)
		}
	}
	_, err = s.db.Exec("DELETE FROM projects WHERE id = ?", id)
	return err
}

func (s *Projects) removeStackResources(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("stack has no Compose project name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	filter := "label=com.docker.compose.project=" + name
	out, err := s.runDocker(ctx, "ps", "-aq", "--no-trunc", "--filter", filter)
	if err != nil {
		return fmt.Errorf("could not find stack containers: %w", err)
	}
	ids := strings.Fields(string(out))
	// Refuse to remove the app even when an imported stack has an incorrect path.
	if len(ids) > 0 {
		out, err = s.runDocker(ctx, append([]string{"inspect"}, ids...)...)
		if err != nil {
			return fmt.Errorf("could not verify stack containers: %w", err)
		}
		var containers []struct {
			ID     string `json:"Id"`
			Name   string `json:"Name"`
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
		}
		if json.Unmarshal(out, &containers) != nil || len(containers) != len(ids) {
			return fmt.Errorf("invalid stack container inspection")
		}
		hostname, _ := os.Hostname()
		selfName := os.Getenv("DOCKTUN_CONTAINER")
		if selfName == "" {
			selfName = "docktun"
		}
		for _, c := range containers {
			if c.Config.Labels["com.docker.compose.project"] != name {
				return fmt.Errorf("container does not belong to selected stack")
			}
			if c.ID == hostname || strings.HasPrefix(c.ID, hostname) && len(hostname) >= 12 || strings.TrimPrefix(c.Name, "/") == selfName {
				return fmt.Errorf("cannot delete the stack running DockTun")
			}
		}
		if _, err := s.runDocker(ctx, append([]string{"stop", "--time", "10"}, ids...)...); err != nil {
			return fmt.Errorf("could not stop stack containers: %w", err)
		}
		if _, err := s.runDocker(ctx, append([]string{"rm"}, ids...)...); err != nil {
			return fmt.Errorf("could not remove stack containers: %w", err)
		}
	}
	out, err = s.runDocker(ctx, "network", "ls", "-q", "--filter", filter)
	if err != nil {
		return fmt.Errorf("could not find stack networks: %w", err)
	}
	for _, network := range strings.Fields(string(out)) {
		if _, err := s.runDocker(ctx, "network", "rm", network); err != nil {
			return fmt.Errorf("could not remove stack network: %w", err)
		}
	}
	// No -v: named and anonymous volumes, images and external networks survive.
	return nil
}

// DeleteOptions checks the same folder policy enforced again during deletion.
// This is advisory: files can change after the dialog is opened.
type ProjectDeleteOptions struct {
	Path           string `json:"path"`
	CanRemoveFiles bool   `json:"can_remove_files"`
	Reason         string `json:"reason,omitempty"`
}

func (s *Projects) DeleteOptions(id int) (ProjectDeleteOptions, error) {
	project, err := s.GetByID(id)
	if err != nil {
		return ProjectDeleteOptions{}, err
	}
	options := ProjectDeleteOptions{Path: project.Path}
	if err := s.validateProjectRemoval(project.Path); err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			options.Reason = "This folder is not available inside DockTun. You can still remove the stack containers."
		case errors.Is(err, os.ErrPermission):
			options.Reason = "DockTun cannot access this folder. You can still remove the stack containers."
		default:
			options.Reason = err.Error()
		}
		return options, nil
	}
	options.CanRemoveFiles = true
	return options, nil
}

func (s *Projects) validateProjectRemoval(projectPath string) error {
	if strings.TrimSpace(projectPath) == "" {
		return fmt.Errorf("stack has no accessible project directory; leave file removal unchecked")
	}
	target, err := filepath.Abs(projectPath)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("project directory is not accessible inside DockTun; leave file removal unchecked: %w", err)
	}
	if resolved != target {
		return fmt.Errorf("refusing to delete a project directory through a symbolic link")
	}
	// File cleanup is limited to mounted/managed projects, never a host path
	// guessed from container labels. Ancestors of DockTun are protected too.
	roots := []string{"/opt/projects", "./projects"}
	allowed := false
	for _, root := range roots {
		base, _ := filepath.Abs(root)
		rel, err := filepath.Rel(base, target)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("file removal is only available for directories inside DockTun's projects folder; leave it unchecked to remove containers only")
	}
	if s.selfPath != "" {
		self, _ := filepath.Abs(s.selfPath)
		rel, err := filepath.Rel(target, self)
		if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
			return fmt.Errorf("cannot delete DockTun's directory or its parent")
		}
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("project path is not a directory")
	}
	return nil
}

func (s *Projects) Delete(id int) error {
	return s.DeleteProject(id, false, false)
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
	if strings.TrimSpace(path) == "" {
		s.appendOutput(id, "[ERROR] project directory is missing")
		return
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		s.appendOutput(id, "[ERROR] resolve project directory: "+err.Error())
		return
	}
	path = absolute
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		f := filepath.Join(path, name)
		if fileExists(f) {
			SanitizeComposeFile(f)
			break
		}
	}

	projectName := ""
	if p, err := s.GetByID(id); err == nil {
		projectName = p.Name
	}
	cmd, err := composeProjectCommand(projectName, path, args...)
	if err != nil {
		s.appendOutput(id, "[ERROR] "+err.Error())
		return
	}

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
	if err := cmd.Wait(); err != nil {
		s.appendOutput(id, "[ERROR] compose command exited with error: "+err.Error())
	}
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

// Open a filesystem root so symlink resolution cannot escape the selected stack.
// Only an explicit create/save operation may create a missing project directory.
func (s *Projects) projectRoot(id int, create bool) (*os.Root, error) {
	p, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(p.Path, 0755); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(p.Path)
	if err != nil {
		return nil, fmt.Errorf("project folder is unavailable inside DockTun; mount the stack folder before accessing files: %w", err)
	}
	return root, nil
}

func (s *Projects) ListFiles(id int, relPath string) ([]FileInfo, error) {
	rel, err := transferPath(relPath)
	if err != nil {
		return nil, err
	}
	root, err := s.projectRoot(id, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	files := []FileInfo{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, FileInfo{
			Name: entry.Name(), Path: filepath.ToSlash(filepath.Join(rel, entry.Name())),
			IsDir: entry.IsDir(), Size: info.Size(), ModTime: info.ModTime().Format(time.RFC3339),
		})
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
	rel, err := transferPath(relPath)
	if err != nil {
		return "", err
	}
	root, err := s.projectRoot(id, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	info, err := root.Stat(rel)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("only regular project files can be edited")
	}
	file, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("only regular project files can be edited")
	}
	data, err := io.ReadAll(io.LimitReader(file, 5*1024*1024+1))
	if err != nil {
		return "", err
	}
	if len(data) > 5*1024*1024 {
		return "", fmt.Errorf("file is too large (> 5MB)")
	}
	return string(data), nil
}

func (s *Projects) SaveFile(id int, relPath string, content string) error {
	rel, err := transferPath(relPath)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("cannot replace project root directory")
	}
	root, err := s.projectRoot(id, true)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(rel), 0755); err != nil {
		return err
	}
	return root.WriteFile(rel, []byte(content), 0644)
}

func (s *Projects) DeleteFile(id int, relPath string) error {
	rel, err := transferPath(relPath)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("cannot delete project root directory")
	}
	root, err := s.projectRoot(id, false)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(rel)
}

func (s *Projects) CreateFile(id int, relPath string, isDir bool, content string) error {
	if !isDir {
		return s.SaveFile(id, relPath, content)
	}
	rel, err := transferPath(relPath)
	if err != nil {
		return err
	}
	root, err := s.projectRoot(id, true)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.MkdirAll(rel, 0755)
}

func (s *Projects) GetComposeFile(id int) (string, string, error) {
	p, err := s.GetByID(id)
	if err != nil {
		return "", "", err
	}
	candidates := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
	for _, candidate := range candidates {
		fullPath := filepath.Join(p.Path, candidate)
		content, err := s.GetFile(id, candidate)
		if err == nil {
			return candidate, content, nil
		}
		if !os.IsNotExist(err) {
			return "", "", fmt.Errorf("cannot read Compose file %s: %w", fullPath, err)
		}
	}
	info, err := os.Stat(p.Path)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("stack folder %s is not available inside DockTun; mount the host's project folder before editing its Compose file", p.Path)
	}

	defaultTmpl := fmt.Sprintf(`services:
  %s:
    image: nginx:alpine
    container_name: %s
    restart: unless-stopped
    ports:
      - "8080:80"
`, p.Name, p.Name)
	return "docker-compose.yml", defaultTmpl, nil
}

func (s *Projects) SaveComposeFile(id int, filename string, content string) error {
	if filename == "" {
		filename = "docker-compose.yml"
	}
	if IsDockerRunCommand(content) {
		if parsed, err := ParseDockerRunCommand(content); err == nil {
			content = parsed.GenerateComposeYAML()
		}
	}
	return s.SaveFile(id, filename, content)
}

func (s *Projects) GetEnvFile(id int) (string, error) {
	content, err := s.GetFile(id, ".env")
	if err == nil {
		return content, nil
	}
	if os.IsNotExist(err) {
		project, projectErr := s.GetByID(id)
		if projectErr != nil {
			return "", projectErr
		}
		if info, statErr := os.Stat(project.Path); statErr == nil && info.IsDir() {
			return "", nil // An optional .env may be absent in an accessible stack.
		}
	}
	return "", fmt.Errorf("cannot read stack .env; ensure the project folder is mounted inside DockTun: %w", err)
}

func (s *Projects) SaveEnvFile(id int, content string) error {
	return s.SaveFile(id, ".env", content)
}
