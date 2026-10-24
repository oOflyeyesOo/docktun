package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SizeStr   string `json:"size_str"`
	Content   string `json:"content"`
	IsBinary  bool   `json:"is_binary"`
	MimeType  string `json:"mime_type"`
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
	}

	return targets, nil
}

// -----------------------------------------------------------------------------
// 2. Directory Listing
// -----------------------------------------------------------------------------

func (s *StorageFilesService) ListFiles(targetType, targetID, relPath string) ([]FileNode, error) {
	relPath = cleanRelPath(relPath)

	switch targetType {
	case "docktun", "project":
		basePath := s.resolveHostBasePath(targetType, targetID)
		fullPath := filepath.Join(basePath, relPath)
		return listLocalDirectory(fullPath, relPath)

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
	cmdStr := fmt.Sprintf(`cd "%s" 2>/dev/null || exit 1; for f in * .[!.]* ..?*; do [ -e "$f" ] || continue; if [ -d "$f" ]; then echo "dir|0|$(stat -c %%Y "$f" 2>/dev/null || echo 0)|$f"; else echo "file|$(stat -c %%s "$f" 2>/dev/null || echo 0)|$(stat -c %%Y "$f" 2>/dev/null || echo 0)|$f"; fi; done`, targetDir)

	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:ro", volName, volMount), "alpine:3.19", "sh", "-c", cmdStr)
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

	cmdStr := fmt.Sprintf(`cd "%s" 2>/dev/null || exit 1; for f in * .[!.]* ..?*; do [ -e "$f" ] || continue; if [ -d "$f" ]; then echo "dir|0|$(stat -c %%Y "$f" 2>/dev/null || echo 0)|$f"; else echo "file|$(stat -c %%s "$f" 2>/dev/null || echo 0)|$(stat -c %%Y "$f" 2>/dev/null || echo 0)|$f"; fi; done`, targetDir)

	cmd := exec.CommandContext(ctx, "docker", "exec", containerID, "sh", "-c", cmdStr)
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
	relPath = cleanRelPath(relPath)
	if relPath == "" || relPath == "." {
		return nil, fmt.Errorf("file path required")
	}

	filename := filepath.Base(relPath)

	switch targetType {
	case "docktun", "project":
		basePath := s.resolveHostBasePath(targetType, targetID)
		fullPath := filepath.Join(basePath, relPath)

		fi, err := os.Stat(fullPath)
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("path is a directory")
		}
		if fi.Size() > 5*1024*1024 {
			return nil, fmt.Errorf("file is too large to preview (> 5MB)")
		}

		data, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, err
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

		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:ro", targetID, volMount), "alpine:3.19", "cat", fullPath)
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
	relPath = cleanRelPath(relPath)
	if relPath == "" || relPath == "." {
		return fmt.Errorf("target file path required")
	}

	switch targetType {
	case "docktun", "project":
		basePath := s.resolveHostBasePath(targetType, targetID)
		fullPath := filepath.Join(basePath, relPath)

		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return err
		}
		return os.WriteFile(fullPath, []byte(content), 0644)

	case "volume":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		volMount := "/mnt_vol"
		fullPath := filepath.ToSlash(filepath.Join(volMount, relPath))
		parentDir := filepath.ToSlash(filepath.Dir(fullPath))

		cmdStr := fmt.Sprintf(`mkdir -p "%s" && cat > "%s"`, parentDir, fullPath)
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "-v", fmt.Sprintf("%s:%s:rw", targetID, volMount), "alpine:3.19", "sh", "-c", cmdStr)
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

		cmdStr := fmt.Sprintf(`mkdir -p "%s" && cat > "%s"`, parentDir, fullPath)
		cmd := exec.CommandContext(ctx, "docker", "exec", "-i", targetID, "sh", "-c", cmdStr)
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
	relPath = cleanRelPath(relPath)
	if relPath == "" || relPath == "." {
		return fmt.Errorf("path required")
	}

	switch targetType {
	case "docktun", "project":
		basePath := s.resolveHostBasePath(targetType, targetID)
		fullPath := filepath.Join(basePath, relPath)

		if isDir {
			return os.MkdirAll(fullPath, 0755)
		}
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
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
			cmdStr = fmt.Sprintf(`mkdir -p "%s"`, fullPath)
		} else {
			cmdStr = fmt.Sprintf(`mkdir -p "%s" && touch "%s"`, filepath.ToSlash(filepath.Dir(fullPath)), fullPath)
		}

		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:rw", targetID, volMount), "alpine:3.19", "sh", "-c", cmdStr)
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
			cmdStr = fmt.Sprintf(`mkdir -p "%s"`, fullPath)
		} else {
			cmdStr = fmt.Sprintf(`mkdir -p "%s" && touch "%s"`, filepath.ToSlash(filepath.Dir(fullPath)), fullPath)
		}

		cmd := exec.CommandContext(ctx, "docker", "exec", targetID, "sh", "-c", cmdStr)
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
	relPath = cleanRelPath(relPath)
	if relPath == "" || relPath == "." || relPath == "/" {
		return fmt.Errorf("cannot delete root path")
	}

	switch targetType {
	case "docktun", "project":
		basePath := s.resolveHostBasePath(targetType, targetID)
		fullPath := filepath.Join(basePath, relPath)

		cleanBase := filepath.Clean(basePath)
		cleanTarget := filepath.Clean(fullPath)
		if cleanTarget == cleanBase || !strings.HasPrefix(cleanTarget, cleanBase+string(filepath.Separator)) {
			return fmt.Errorf("access denied: cannot delete outside root")
		}

		return os.RemoveAll(fullPath)

	case "volume":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		volMount := "/mnt_vol"
		fullPath := filepath.ToSlash(filepath.Join(volMount, relPath))

		cmdStr := fmt.Sprintf(`rm -rf "%s"`, fullPath)
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:rw", targetID, volMount), "alpine:3.19", "sh", "-c", cmdStr)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to delete in volume: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	case "container":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		fullPath := "/" + strings.TrimPrefix(relPath, "/")
		cmdStr := fmt.Sprintf(`rm -rf "%s"`, fullPath)
		cmd := exec.CommandContext(ctx, "docker", "exec", targetID, "sh", "-c", cmdStr)
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

func (s *StorageFilesService) GetDownloadStream(targetType, targetID, relPath string) (io.ReadCloser, string, int64, error) {
	relPath = cleanRelPath(relPath)
	filename := filepath.Base(relPath)

	switch targetType {
	case "docktun", "project":
		basePath := s.resolveHostBasePath(targetType, targetID)
		fullPath := filepath.Join(basePath, relPath)

		fi, err := os.Stat(fullPath)
		if err != nil {
			return nil, "", 0, err
		}
		f, err := os.Open(fullPath)
		if err != nil {
			return nil, "", 0, err
		}
		return f, filename, fi.Size(), nil

	case "volume":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		volMount := "/mnt_vol"
		fullPath := filepath.ToSlash(filepath.Join(volMount, relPath))

		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-v", fmt.Sprintf("%s:%s:ro", targetID, volMount), "alpine:3.19", "cat", fullPath)
		data, err := cmd.Output()
		if err != nil {
			return nil, "", 0, fmt.Errorf("failed to read file: %w", err)
		}

		rc := io.NopCloser(bytes.NewReader(data))
		return rc, filename, int64(len(data)), nil

	case "container":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		fullPath := "/" + strings.TrimPrefix(relPath, "/")
		cmd := exec.CommandContext(ctx, "docker", "exec", targetID, "cat", fullPath)
		data, err := cmd.Output()
		if err != nil {
			return nil, "", 0, fmt.Errorf("failed to read file: %w", err)
		}

		rc := io.NopCloser(bytes.NewReader(data))
		return rc, filename, int64(len(data)), nil

	default:
		return nil, "", 0, fmt.Errorf("unknown target type: %s", targetType)
	}
}

// -----------------------------------------------------------------------------
// 8. Upload File
// -----------------------------------------------------------------------------

func (s *StorageFilesService) UploadFile(targetType, targetID, targetDir, filename string, reader io.Reader) error {
	targetDir = cleanRelPath(targetDir)
	targetPath := filepath.ToSlash(filepath.Join(targetDir, filename))

	switch targetType {
	case "docktun", "project":
		basePath := s.resolveHostBasePath(targetType, targetID)
		fullPath := filepath.Join(basePath, targetPath)

		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return err
		}
		dst, err := os.Create(fullPath)
		if err != nil {
			return err
		}
		defer dst.Close()
		_, err = io.Copy(dst, reader)
		return err

	case "volume":
		// Read uploaded bytes into buffer
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		volMount := "/mnt_vol"
		fullPath := filepath.ToSlash(filepath.Join(volMount, targetPath))
		parentDir := filepath.ToSlash(filepath.Dir(fullPath))

		cmdStr := fmt.Sprintf(`mkdir -p "%s" && cat > "%s"`, parentDir, fullPath)
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "-v", fmt.Sprintf("%s:%s:rw", targetID, volMount), "alpine:3.19", "sh", "-c", cmdStr)
		cmd.Stdin = bytes.NewReader(data)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to upload into volume: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	case "container":
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		fullPath := "/" + strings.TrimPrefix(targetPath, "/")
		parentDir := filepath.ToSlash(filepath.Dir(fullPath))

		cmdStr := fmt.Sprintf(`mkdir -p "%s" && cat > "%s"`, parentDir, fullPath)
		cmd := exec.CommandContext(ctx, "docker", "exec", "-i", targetID, "sh", "-c", cmdStr)
		cmd.Stdin = bytes.NewReader(data)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to upload into container: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil

	default:
		return fmt.Errorf("unknown target type: %s", targetType)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *StorageFilesService) resolveHostBasePath(targetType, targetID string) string {
	if targetType == "docktun" {
		switch targetID {
		case "app_data":
			return "/app/data"
		case "cloudflared":
			return "/etc/cloudflared"
		case "projects":
			return "/opt/projects"
		default:
			return "/app/data"
		}
	}

	if targetType == "project" {
		pid, _ := strconv.Atoi(targetID)
		var path string
		_ = s.db.QueryRow("SELECT path FROM projects WHERE id = ?", pid).Scan(&path)
		if path != "" {
			return path
		}
		return "/opt/projects"
	}

	return "/app/data"
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
