package service

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"docktun/internal/config"
	"docktun/internal/db"
)

type StorageTarget struct {
	Type        string `json:"type"`        // "volume", "container", "project", "docktun"
	ID          string `json:"id"`          // Volume name, Container ID/Name, Project ID
	Name        string `json:"name"`        // Friendly title
	Description string `json:"description"` // Subtitle
	RootPath    string `json:"root_path"`   // Base root
}

type FileNode struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	Size        int64  `json:"size"`
	SizeStr     string `json:"size_str"`
	ModTime     string `json:"mod_time"`
	Permissions string `json:"permissions"`
	Extension   string `json:"extension"`
}

type FileReadResult struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SizeStr  string `json:"size_str"`
	Content  string `json:"content"`
	IsBinary bool   `json:"is_binary"`
	MimeType string `json:"mime_type"`
}

type StorageFilesService struct {
	db  *db.DB
	cfg *config.Config
}

func NewStorageFilesService(database *db.DB, cfg *config.Config) *StorageFilesService {
	return &StorageFilesService{
		db:  database,
		cfg: cfg,
	}
}

// -----------------------------------------------------------------------------
// 1. Storage Targets Discovery
// -----------------------------------------------------------------------------

func (s *StorageFilesService) ListTargets() ([]StorageTarget, error) {
	var targets []StorageTarget

	// 1. Host System Locations
	targets = append(targets, StorageTarget{
		Type:        "docktun",
		ID:          "app_data",
		Name:        "DockTun Data (/app/data)",
		Description: "SQLite DB (app.db), JWT Secret, Tailscale/Wireguard state",
		RootPath:    "/app/data",
	})
	targets = append(targets, StorageTarget{
		Type:        "docktun",
		ID:          "cloudflared",
		Name:        "Cloudflared Config (/etc/cloudflared)",
		Description: "config.yml, tunnel credentials, automated YAML backups",
		RootPath:    "/etc/cloudflared",
	})
	targets = append(targets, StorageTarget{
		Type:        "docktun",
		ID:          "projects",
		Name:        "Projects Root (/opt/projects)",
		Description: "Host directory containing all Docker Compose stacks",
		RootPath:    "/opt/projects",
	})

	// 2. Docker Volumes
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	volCmd := exec.CommandContext(ctx, "docker", "volume", "ls", "--format", "{{.Name}}\t{{.Driver}}")
	if out, err := volCmd.Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, l := range lines {
			parts := strings.Split(l, "\t")
			if len(parts) >= 1 && strings.TrimSpace(parts[0]) != "" {
				name := strings.TrimSpace(parts[0])
				driver := "local"
				if len(parts) >= 2 {
					driver = strings.TrimSpace(parts[1])
				}
				desc := fmt.Sprintf("Docker Volume (%s)", driver)
				if len(name) == 64 {
					desc = "Anonymous Docker Volume (Hash)"
				}
				targets = append(targets, StorageTarget{
					Type:        "volume",
					ID:          name,
					Name:        name,
					Description: desc,
					RootPath:    "/",
				})
			}
		}
	}

	// 3. Docker Containers
	psCtx, psCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer psCancel()

	psCmd := exec.CommandContext(psCtx, "docker", "ps", "-a", "--format", "{{.ID}}\t{{.Names}}\t{{.State}}\t{{.Image}}")
	if out, err := psCmd.Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, l := range lines {
			parts := strings.Split(l, "\t")
			if len(parts) >= 4 {
				cID := strings.TrimSpace(parts[0])
				cName := strings.TrimPrefix(strings.TrimSpace(parts[1]), "/")
				state := strings.TrimSpace(parts[2])
				img := strings.TrimSpace(parts[3])

				targets = append(targets, StorageTarget{
					Type:        "container",
					ID:          cName,
					Name:        cName,
					Description: fmt.Sprintf("Container (%s • %s • %s)", state, img, cID[:min(12, len(cID))]),
					RootPath:    "/",
				})
			}
		}
	}

	// 4. Managed Projects from SQLite
	rows, err := s.db.Query("SELECT id, name, path FROM projects ORDER BY name")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var pid int
			var pname, ppath string
			if err := rows.Scan(&pid, &pname, &ppath); err == nil {
				targets = append(targets, StorageTarget{
					Type:        "project",
					ID:          strconv.Itoa(pid),
					Name:        pname,
					Description: fmt.Sprintf("Compose Stack (%s)", ppath),
					RootPath:    ppath,
				})
			}
		}
		if scanErr := rows.Err(); scanErr != nil {
			log.Printf("storage_files ListTargets projects error: %v", scanErr)
		}
	}

	return targets, nil
}

// -----------------------------------------------------------------------------
// 2. Directory Listing
// -----------------------------------------------------------------------------

func (s *StorageFilesService) ListFiles(targetType, targetID, relPath string) ([]FileNode, error) {
	if _, err := transferPath(relPath); err != nil {
		return nil, err
	}
	if err := transferTarget(targetType, targetID); err != nil {
		return nil, err
	}
	relPath = cleanRelPath(relPath)

	switch targetType {
	case "docktun", "project":
		basePath, err := s.resolveHostBasePath(targetType, targetID)
		if err != nil {
			return nil, err
		}
		root, err := os.OpenRoot(basePath)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		directory := relPath
		if directory == "" {
			directory = "."
		}
		file, err := root.Open(directory)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		entries, err := file.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		return localDirectoryNodes(entries, relPath)

	case "volume":
		return s.listVolumeDirectory(targetID, relPath)

	case "container":
		return s.listContainerDirectory(targetID, relPath)

	default:
		return nil, fmt.Errorf("unknown target type: %s", targetType)
	}
}

func listLocalDirectory(fullPath, relPath string) ([]FileNode, error) {
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	return localDirectoryNodes(entries, relPath)
}

func localDirectoryNodes(entries []os.DirEntry, relPath string) ([]FileNode, error) {
	var nodes []FileNode
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}

		childRel := filepath.ToSlash(filepath.Join(relPath, e.Name()))
		sz := info.Size()
		if e.IsDir() {
			sz = 0
		}

		nodes = append(nodes, FileNode{
			Name:        e.Name(),
			Path:        childRel,
			IsDir:       e.IsDir(),
			Size:        sz,
			SizeStr:     formatBytes(sz),
			ModTime:     info.ModTime().Format("2006-01-02 15:04:05"),
			Permissions: info.Mode().String(),
			Extension:   strings.ToLower(filepath.Ext(e.Name())),
		})
	}

	sortNodes(nodes)
	return nodes, nil
}

func (s *StorageFilesService) listVolumeDirectory(volName, relPath string) ([]FileNode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	volMount := "/mnt_vol"
	targetDir := volMount
	if relPath != "" && relPath != "/" && relPath != "." {
		targetDir = filepath.ToSlash(filepath.Join(volMount, relPath))
	}

	// Run lightweight alpine runner with volume mounted read-only
	// Output format: type|size|time|name
	cmdStr := `cd "$1" 2>/dev/null || exit 1; for f in * .[!.]* ..?*; do [ -e "$f" ] || continue; if [ -d "$f" ]; then echo "dir|0|$(stat -c %Y "$f" 2>/dev/null || echo 0)|$f"; else echo "file|$(stat -c %s "$f" 2>/dev/null || echo 0)|$(stat -c %Y "$f" 2>/dev/null || echo 0)|$f"; fi; done`

	runnerImg := s.getRunnerImage()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:ro", volName, volMount), runnerImg, "sh", "-c", cmdStr, "storage", targetDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// If dir is empty or didn't exist
		if strings.Contains(string(out), "can't cd") || strings.Contains(string(out), "No such file") {
			return nil, fmt.Errorf("directory not found: %s", relPath)
		}
		// An empty folder produces no output and might exit with 0
		if len(strings.TrimSpace(string(out))) == 0 {
			return []FileNode{}, nil
		}
	}

	return parseShellListingOutput(string(out), relPath), nil
}

func (s *StorageFilesService) listContainerDirectory(containerID, relPath string) ([]FileNode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	targetDir := "/"
	if relPath != "" && relPath != "/" && relPath != "." {
		targetDir = "/" + strings.TrimPrefix(relPath, "/")
	}

	cmdStr := `cd "$1" 2>/dev/null || exit 1; for f in * .[!.]* ..?*; do [ -e "$f" ] || continue; if [ -d "$f" ]; then echo "dir|0|$(stat -c %Y "$f" 2>/dev/null || echo 0)|$f"; else echo "file|$(stat -c %s "$f" 2>/dev/null || echo 0)|$(stat -c %Y "$f" 2>/dev/null || echo 0)|$f"; fi; done`

	cmd := exec.CommandContext(ctx, "docker", "exec", containerID, "sh", "-c", cmdStr, "storage", targetDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback for containers without stat or custom shell: use ls -la
		cmdFallback := exec.CommandContext(ctx, "docker", "exec", containerID, "ls", "-1a", targetDir)
		outFb, errFb := cmdFallback.CombinedOutput()
		if errFb != nil {
			return nil, fmt.Errorf("cannot browse container %s: %s", containerID, strings.TrimSpace(string(out)))
		}
		return parseSimpleLsOutput(string(outFb), relPath), nil
	}

	return parseShellListingOutput(string(out), relPath), nil
}

func parseShellListingOutput(raw, relPath string) []FileNode {
	var nodes []FileNode
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		parts := strings.SplitN(l, "|", 4)
		if len(parts) < 4 {
			continue
		}

		isDir := parts[0] == "dir"
		sz, _ := strconv.ParseInt(parts[1], 10, 64)
		epoch, _ := strconv.ParseInt(parts[2], 10, 64)
		name := parts[3]

		if name == "." || name == ".." {
			continue
		}

		modTimeStr := ""
		if epoch > 0 {
			modTimeStr = time.Unix(epoch, 0).Format("2006-01-02 15:04:05")
		}

		childRel := filepath.ToSlash(filepath.Join(relPath, name))

		nodes = append(nodes, FileNode{
			Name:        name,
			Path:        childRel,
			IsDir:       isDir,
			Size:        sz,
			SizeStr:     formatBytes(sz),
			ModTime:     modTimeStr,
			Permissions: "0644",
			Extension:   strings.ToLower(filepath.Ext(name)),
		})
	}

	sortNodes(nodes)
	return nodes
}

func parseSimpleLsOutput(raw, relPath string) []FileNode {
	var nodes []FileNode
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || name == "." || name == ".." {
			continue
		}
		childRel := filepath.ToSlash(filepath.Join(relPath, name))
		nodes = append(nodes, FileNode{
			Name:      name,
			Path:      childRel,
			IsDir:     !strings.Contains(name, "."),
			SizeStr:   "N/A",
			Extension: strings.ToLower(filepath.Ext(name)),
		})
	}
	sortNodes(nodes)
	return nodes
}

func sortNodes(nodes []FileNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsDir != nodes[j].IsDir {
			return nodes[i].IsDir // Dirs first
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
}

// -----------------------------------------------------------------------------
// 3. Read File Content
// -----------------------------------------------------------------------------

func (s *StorageFilesService) ReadFile(targetType, targetID, relPath string) (*FileReadResult, error) {
	if _, err := transferPath(relPath); err != nil {
		return nil, err
	}
	if err := transferTarget(targetType, targetID); err != nil {
		return nil, err
	}
	relPath = cleanRelPath(relPath)
	if relPath == "" || relPath == "." {
		return nil, fmt.Errorf("file path required")
	}

	filename := filepath.Base(relPath)

	switch targetType {
	case "docktun", "project":
		basePath, err := s.resolveHostBasePath(targetType, targetID)
		if err != nil {
			return nil, err
		}
		root, err := os.OpenRoot(basePath)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		file, err := root.Open(relPath)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		fi, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("path is a directory")
		}
		if fi.Size() > 5*1024*1024 {
			return nil, fmt.Errorf("file is too large to preview (> 5MB)")
		}

		data, err := io.ReadAll(io.LimitReader(file, 5*1024*1024+1))
		if err != nil {
			return nil, err
		}

		if len(data) > 5*1024*1024 {
			return nil, fmt.Errorf("file is too large to preview (> 5MB)")
		}
		isBin := isBinaryContent(data)
		content := ""
		if !isBin {
			content = string(data)
		}

		return &FileReadResult{
			Path:     relPath,
			Name:     filename,
			Size:     fi.Size(),
			SizeStr:  formatBytes(fi.Size()),
			Content:  content,
			IsBinary: isBin,
		}, nil

	case "volume":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		volMount := "/mnt_vol"
		fullPath := filepath.ToSlash(filepath.Join(volMount, relPath))

		runnerImg := s.getRunnerImage()
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:ro", targetID, volMount), runnerImg, "cat", fullPath)
		data, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to read file from volume: %w", err)
		}

		if len(data) > 5*1024*1024 {
			return nil, fmt.Errorf("file is too large to preview (> 5MB)")
		}

		isBin := isBinaryContent(data)
		content := ""
		if !isBin {
			content = string(data)
		}

		return &FileReadResult{
			Path:     relPath,
			Name:     filename,
			Size:     int64(len(data)),
			SizeStr:  formatBytes(int64(len(data))),
			Content:  content,
			IsBinary: isBin,
		}, nil

	case "container":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		fullPath := "/" + strings.TrimPrefix(relPath, "/")
		cmd := exec.CommandContext(ctx, "docker", "exec", targetID, "cat", fullPath)
		data, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to read file from container %s: %w", targetID, err)
		}

		if len(data) > 5*1024*1024 {
			return nil, fmt.Errorf("file is too large to preview (> 5MB)")
		}

		isBin := isBinaryContent(data)
		content := ""
		if !isBin {
			content = string(data)
		}

		return &FileReadResult{
			Path:     relPath,
			Name:     filename,
			Size:     int64(len(data)),
			SizeStr:  formatBytes(int64(len(data))),
			Content:  content,
			IsBinary: isBin,
		}, nil

	default:
		return nil, fmt.Errorf("unknown target type: %s", targetType)
	}
}

// -----------------------------------------------------------------------------
// 4. Write / Save File Content
// -----------------------------------------------------------------------------

func (s *StorageFilesService) WriteFile(targetType, targetID, relPath, content string) error {
	if _, err := transferPath(relPath); err != nil {
		return err
	}
	if err := transferTarget(targetType, targetID); err != nil {
		return err
	}
	relPath = cleanRelPath(relPath)
	if relPath == "" || relPath == "." {
		return fmt.Errorf("target file path required")
	}

	switch targetType {
	case "docktun", "project":
		basePath, err := s.resolveHostBasePath(targetType, targetID)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(basePath)
		if err != nil {
			return err
		}
		defer root.Close()
		if err := root.MkdirAll(filepath.Dir(relPath), 0755); err != nil {
			return err
		}
		return root.WriteFile(relPath, []byte(content), 0644)

	case "volume":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		volMount := "/mnt_vol"
		fullPath := filepath.ToSlash(filepath.Join(volMount, relPath))
		parentDir := filepath.ToSlash(filepath.Dir(fullPath))

		cmdStr := `mkdir -p "$1" && cat > "$2"`
		runnerImg := s.getRunnerImage()
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "-v", fmt.Sprintf("%s:%s:rw", targetID, volMount), runnerImg, "sh", "-c", cmdStr, "storage", parentDir, fullPath)
		cmd.Stdin = strings.NewReader(content)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to write file to volume: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	case "container":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		fullPath := "/" + strings.TrimPrefix(relPath, "/")
		parentDir := filepath.ToSlash(filepath.Dir(fullPath))

		cmdStr := `mkdir -p "$1" && cat > "$2"`
		cmd := exec.CommandContext(ctx, "docker", "exec", "-i", targetID, "sh", "-c", cmdStr, "storage", parentDir, fullPath)
		cmd.Stdin = strings.NewReader(content)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to write file to container: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	default:
		return fmt.Errorf("unknown target type: %s", targetType)
	}
}

// -----------------------------------------------------------------------------
// 5. Create File or Directory
// -----------------------------------------------------------------------------

func (s *StorageFilesService) CreateItem(targetType, targetID, relPath string, isDir bool) error {
	if _, err := transferPath(relPath); err != nil {
		return err
	}
	if err := transferTarget(targetType, targetID); err != nil {
		return err
	}
	relPath = cleanRelPath(relPath)
	if relPath == "" || relPath == "." {
		return fmt.Errorf("path required")
	}

	switch targetType {
	case "docktun", "project":
		basePath, err := s.resolveHostBasePath(targetType, targetID)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(basePath)
		if err != nil {
			return err
		}
		defer root.Close()
		if isDir {
			return root.MkdirAll(relPath, 0755)
		}
		if err := root.MkdirAll(filepath.Dir(relPath), 0755); err != nil {
			return err
		}
		f, err := root.OpenFile(relPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		return f.Close()

	case "volume":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		volMount := "/mnt_vol"
		fullPath := filepath.ToSlash(filepath.Join(volMount, relPath))

		var cmdStr string
		if isDir {
			cmdStr = `mkdir -p -- "$2"`
		} else {
			cmdStr = `mkdir -p -- "$1" && touch -- "$2"`
		}

		runnerImg := s.getRunnerImage()
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:rw", targetID, volMount), runnerImg, "sh", "-c", cmdStr, "storage", filepath.ToSlash(filepath.Dir(fullPath)), fullPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to create in volume: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	case "container":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		fullPath := "/" + strings.TrimPrefix(relPath, "/")
		var cmdStr string
		if isDir {
			cmdStr = `mkdir -p -- "$2"`
		} else {
			cmdStr = `mkdir -p -- "$1" && touch -- "$2"`
		}

		cmd := exec.CommandContext(ctx, "docker", "exec", targetID, "sh", "-c", cmdStr, "storage", filepath.ToSlash(filepath.Dir(fullPath)), fullPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to create in container: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	default:
		return fmt.Errorf("unknown target type: %s", targetType)
	}
}

// -----------------------------------------------------------------------------
// 6. Delete File or Directory
// -----------------------------------------------------------------------------

func (s *StorageFilesService) DeleteItem(targetType, targetID, relPath string) error {
	if _, err := transferPath(relPath); err != nil {
		return err
	}
	if err := transferTarget(targetType, targetID); err != nil {
		return err
	}
	relPath = cleanRelPath(relPath)
	if relPath == "" || relPath == "." || relPath == "/" {
		return fmt.Errorf("cannot delete root path")
	}

	switch targetType {
	case "docktun", "project":
		basePath, err := s.resolveHostBasePath(targetType, targetID)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(basePath)
		if err != nil {
			return err
		}
		defer root.Close()
		return root.RemoveAll(relPath)

	case "volume":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		volMount := "/mnt_vol"
		fullPath := filepath.ToSlash(filepath.Join(volMount, relPath))

		cmdStr := `rm -rf -- "$2"`
		runnerImg := s.getRunnerImage()
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:rw", targetID, volMount), runnerImg, "sh", "-c", cmdStr, "storage", filepath.ToSlash(filepath.Dir(fullPath)), fullPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to delete in volume: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	case "container":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		fullPath := "/" + strings.TrimPrefix(relPath, "/")
		cmdStr := `rm -rf -- "$2"`
		cmd := exec.CommandContext(ctx, "docker", "exec", targetID, "sh", "-c", cmdStr, "storage", filepath.ToSlash(filepath.Dir(fullPath)), fullPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to delete in container: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	default:
		return fmt.Errorf("unknown target type: %s", targetType)
	}
}

// -----------------------------------------------------------------------------
// 7. Download File Stream
// -----------------------------------------------------------------------------

// Helpers
// -----------------------------------------------------------------------------

func (s *StorageFilesService) getRunnerImage() string {
	candidates := []string{"alpine:3.19", "alpine:3.20", "alpine:3.21", "alpine:3.22", "alpine:3.23", "alpine:latest", "alpine", "busybox"}
	for _, img := range candidates {
		if exec.Command("docker", "image", "inspect", img).Run() == nil {
			return img
		}
	}
	return "alpine:3.19"
}

func (s *StorageFilesService) resolveHostBasePath(targetType, targetID string) (string, error) {
	if targetType == "docktun" {
		switch targetID {
		case "app_data":
			return "/app/data", nil
		case "cloudflared":
			return "/etc/cloudflared", nil
		case "projects":
			return "/opt/projects", nil
		}
		return "", fmt.Errorf("unknown DockTun storage target")
	}
	if targetType == "project" {
		pid, err := strconv.Atoi(targetID)
		if err != nil || pid <= 0 || s.db == nil {
			return "", fmt.Errorf("invalid project storage target")
		}
		var directory string
		if err := s.db.QueryRow("SELECT path FROM projects WHERE id = ?", pid).Scan(&directory); err != nil {
			return "", fmt.Errorf("project storage target is unavailable: %w", err)
		}
		if directory == "" {
			return "", fmt.Errorf("project has no storage folder")
		}
		return directory, nil
	}
	return "", fmt.Errorf("unknown local storage target type")
}

func cleanRelPath(p string) string {
	p = strings.TrimSpace(p)
	p = filepath.Clean(p)
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimPrefix(p, "\\")
	if p == "." {
		return ""
	}
	return filepath.ToSlash(p)
}

func isBinaryContent(data []byte) bool {
	checkLen := min(len(data), 512)
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
