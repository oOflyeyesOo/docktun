package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"docktun/internal/config"
	"docktun/internal/db"
	"gopkg.in/yaml.v3"
)

const (
	NobodyUID = 65534
	NobodyGID = 65534
)

type StorageDoctorReport struct {
	Healthy        bool                  `json:"healthy"`
	ChecksRun      int                   `json:"checks_run"`
	ErrorCount     int                   `json:"error_count"`
	WarningCount   int                   `json:"warning_count"`
	IsLXCContainer bool                  `json:"is_lxc_container"`
	LXCDetails     string                `json:"lxc_details,omitempty"`
	Summary        string                `json:"summary"`
	Checks         []StorageDoctorCheck  `json:"checks"`
	Remediations   []StorageDoctorRemedy `json:"remediations"`
	Timestamp      string                `json:"timestamp"`
}

type StorageDoctorCheck struct {
	Category    string `json:"category"` // "database", "cloudflared", "project", "mount", "system"
	Name        string `json:"name"`
	Path        string `json:"path"`
	Status      string `json:"status"` // "ok", "warning", "error"
	Exists      bool   `json:"exists"`
	Readable    bool   `json:"readable"`
	Writable    bool   `json:"writable"`
	UID         int    `json:"uid"`
	GID         int    `json:"gid"`
	Owner       string `json:"owner"`
	Permissions string `json:"permissions"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type StorageDoctorRemedy struct {
	Target      string `json:"target"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

type StorageDoctor struct {
	db             *db.DB
	cfg            *config.Config
	probeOwnership func(fi os.FileInfo) (int, int, bool)
	uidMapPath     string
	cgroupPath     string
	mu             sync.Mutex
}

func NewStorageDoctor(database *db.DB, cfg *config.Config) *StorageDoctor {
	return &StorageDoctor{
		db:             database,
		cfg:            cfg,
		probeOwnership: getSystemFileOwnership,
		uidMapPath:     "/proc/self/uid_map",
		cgroupPath:     "/proc/self/cgroup",
	}
}

// SetOwnershipProbe allows tests to mock UID/GID resolution on non-Linux hosts.
func (d *StorageDoctor) SetOwnershipProbe(probe func(fi os.FileInfo) (int, int, bool)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.probeOwnership = probe
}

// SetProcPaths allows tests to mock LXC detection files.
func (d *StorageDoctor) SetProcPaths(uidMap, cgroup string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.uidMapPath = uidMap
	d.cgroupPath = cgroup
}

func (d *StorageDoctor) getOwnership(fi os.FileInfo) (int, int, bool) {
	d.mu.Lock()
	probe := d.probeOwnership
	d.mu.Unlock()
	if probe != nil {
		return probe(fi)
	}
	return getSystemFileOwnership(fi)
}

// DetectLXC checks if running inside an unprivileged LXC container.
func (d *StorageDoctor) DetectLXC() (bool, string) {
	d.mu.Lock()
	uidMapFile := d.uidMapPath
	cgroupFile := d.cgroupPath
	d.mu.Unlock()

	// Check /proc/self/uid_map for unprivileged user namespace mapping
	if data, err := os.ReadFile(uidMapFile); err == nil {
		content := strings.TrimSpace(string(data))
		fields := strings.Fields(content)
		// Format: <inside-uid> <host-uid> <count>
		// e.g., "0 100000 65536"
		if len(fields) >= 3 {
			if fields[0] == "0" && fields[1] != "0" {
				return true, fmt.Sprintf("Proxmox unprivileged LXC container detected (host subuid base: %s, count: %s)", fields[1], fields[2])
			}
		}
	}

	// Check cgroup or container env
	if data, err := os.ReadFile(cgroupFile); err == nil {
		if strings.Contains(string(data), "lxc") {
			return true, "LXC container environment detected via cgroups"
		}
	}

	if val := os.Getenv("container"); val == "lxc" {
		return true, "LXC environment detected via container environment variable"
	}

	return false, ""
}

// Diagnose runs a comprehensive permissions scan across all critical storage targets.
func (d *StorageDoctor) Diagnose(ctx context.Context) *StorageDoctorReport {
	report := &StorageDoctorReport{
		Healthy:   true,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    []StorageDoctorCheck{},
	}

	isLXC, lxcDetails := d.DetectLXC()
	report.IsLXCContainer = isLXC
	report.LXCDetails = lxcDetails

	// 1. Inspect Database Storage
	d.checkDatabaseStorage(report, isLXC)

	// 2. Inspect Cloudflared Config Storage
	d.checkCloudflaredStorage(report, isLXC)

	// 3. Inspect Default Projects Base Directory
	d.checkProjectsBase(report, isLXC)

	// 4. Inspect Registered Projects and Bind Mounts
	d.checkRegisteredProjects(report, isLXC)

	// Consolidate report metrics and remediations
	remedyMap := make(map[string]StorageDoctorRemedy)
	for _, chk := range report.Checks {
		report.ChecksRun++
		if chk.Status == "error" {
			report.ErrorCount++
			report.Healthy = false
		} else if chk.Status == "warning" {
			report.WarningCount++
		}

		if chk.Remediation != "" {
			cmd := chk.Remediation
			key := chk.Path
			if _, exists := remedyMap[key]; !exists {
				desc := fmt.Sprintf("Fix permissions on %s (%s)", chk.Name, chk.Path)
				remedyMap[key] = StorageDoctorRemedy{
					Target:      chk.Path,
					Command:     cmd,
					Description: desc,
				}
			}
		}
	}

	for _, rem := range remedyMap {
		report.Remediations = append(report.Remediations, rem)
	}

	if report.Healthy && report.WarningCount == 0 {
		report.Summary = "All storage directories and bind mounts have full read/write permissions. No LXC UID/GID ownership conflicts detected."
	} else if report.ErrorCount > 0 {
		if isLXC {
			report.Summary = fmt.Sprintf("Detected %d storage permission error(s) under Proxmox unprivileged LXC. Host UID/GID mapping adjustments (chown 100000:100000) are required for proper operation.", report.ErrorCount)
		} else {
			report.Summary = fmt.Sprintf("Detected %d storage permission error(s). Ensure DockTun has read and write access to the flagged directories.", report.ErrorCount)
		}
	} else {
		report.Summary = fmt.Sprintf("All storage targets are functional, but %d warning(s) were found (e.g. nobody:nogroup ownership). Review recommended.", report.WarningCount)
	}

	return report
}

func (d *StorageDoctor) checkDatabaseStorage(report *StorageDoctorReport, isLXC bool) {
	dbPath := "/app/data/app.db"
	if d.cfg != nil && d.cfg.DBPath != "" {
		dbPath = d.cfg.DBPath
	}
	dbDir := filepath.Dir(dbPath)

	// Check DB Directory
	dirCheck := d.inspectPath("database", "Database Directory", dbDir, true, isLXC)
	report.Checks = append(report.Checks, dirCheck)

	// Check DB File itself if present
	if _, err := os.Stat(dbPath); err == nil {
		fileCheck := d.inspectPath("database", "SQLite Database File", dbPath, false, isLXC)
		report.Checks = append(report.Checks, fileCheck)
	}
}

func (d *StorageDoctor) checkCloudflaredStorage(report *StorageDoctorReport, isLXC bool) {
	cfgPath := "/etc/cloudflared/config.yml"
	if d.cfg != nil && d.cfg.CloudflaredConfig != "" {
		cfgPath = d.cfg.CloudflaredConfig
	}
	cfgDir := filepath.Dir(cfgPath)

	// Check Cloudflared Directory
	dirCheck := d.inspectPath("cloudflared", "Cloudflared Config Directory", cfgDir, true, isLXC)
	report.Checks = append(report.Checks, dirCheck)

	// Check Cloudflared Config File if present
	if _, err := os.Stat(cfgPath); err == nil {
		fileCheck := d.inspectPath("cloudflared", "Cloudflared Config File", cfgPath, false, isLXC)
		report.Checks = append(report.Checks, fileCheck)
	}
}

func (d *StorageDoctor) checkProjectsBase(report *StorageDoctorReport, isLXC bool) {
	baseDir := ProjectsRoot()
	if baseDir == "" {
		baseDir = "/opt/projects"
	}
	chk := d.inspectPath("system", "Projects Base Directory", baseDir, true, isLXC)
	report.Checks = append(report.Checks, chk)
}

func (d *StorageDoctor) checkRegisteredProjects(report *StorageDoctorReport, isLXC bool) {
	if d.db == nil {
		return
	}

	rows, err := d.db.Query(`SELECT id, name, path FROM projects ORDER BY name`)
	if err != nil {
		return
	}
	defer rows.Close()

	type projItem struct {
		id   int
		name string
		path string
	}
	projects := []projItem{}
	for rows.Next() {
		var p projItem
		if err := rows.Scan(&p.id, &p.name, &p.path); err == nil {
			projects = append(projects, p)
		}
	}

	for _, p := range projects {
		name := fmt.Sprintf("Project: %s", p.name)
		chk := d.inspectPath("project", name, p.path, true, isLXC)
		report.Checks = append(report.Checks, chk)

		// Also check bind mounts from Compose file if available
		d.checkProjectComposeMounts(report, p.id, p.name, p.path, isLXC)
	}
}

func (d *StorageDoctor) checkProjectComposeMounts(report *StorageDoctorReport, projectID int, projectName, projectPath string, isLXC bool) {
	composeFiles := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
	var composePath string
	for _, f := range composeFiles {
		candidate := filepath.Join(projectPath, f)
		if _, err := os.Stat(candidate); err == nil {
			composePath = candidate
			break
		}
	}
	if composePath == "" {
		return
	}

	data, err := os.ReadFile(composePath)
	if err != nil {
		return
	}

	mounts := extractComposeBindMounts(data, projectPath)
	seen := make(map[string]bool)
	for _, m := range mounts {
		if seen[m] {
			continue
		}
		seen[m] = true
		chkName := fmt.Sprintf("Bind Mount: %s (%s)", filepath.Base(m), projectName)
		chk := d.inspectPath("mount", chkName, m, true, isLXC)
		report.Checks = append(report.Checks, chk)
	}
}

// inspectPath checks existence, readability, writability, and UID/GID ownership.
func (d *StorageDoctor) inspectPath(category, name, targetPath string, isDir, isLXC bool) StorageDoctorCheck {
	chk := StorageDoctorCheck{
		Category: category,
		Name:     name,
		Path:     targetPath,
		Status:   "ok",
	}

	fi, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			chk.Exists = false
			chk.Status = "warning"
			chk.Message = "Target path does not exist on filesystem."
			if isDir {
				chk.Remediation = fmt.Sprintf("mkdir -p %s", targetPath)
			}
			return chk
		}
		chk.Status = "error"
		chk.Message = fmt.Sprintf("Cannot stat path: %v", err)
		return chk
	}

	chk.Exists = true
	chk.Permissions = fi.Mode().String()

	// Extract UID/GID
	uid, gid, hasSys := d.getOwnership(fi)
	chk.UID = uid
	chk.GID = gid
	if hasSys {
		if uid == NobodyUID && gid == NobodyGID {
			chk.Owner = "nobody:nogroup (65534:65534)"
		} else {
			chk.Owner = fmt.Sprintf("%d:%d", uid, gid)
		}
	} else {
		chk.Owner = "system"
	}

	// Test Read Access
	chk.Readable = d.testRead(targetPath, fi.IsDir())

	// Test Write Access
	chk.Writable, err = d.testWrite(targetPath, fi.IsDir())

	// Detect LXC unprivileged UID/GID mapping issues (nobody 65534)
	isNobody := (uid == NobodyUID || gid == NobodyGID)

	if !chk.Readable {
		chk.Status = "error"
		chk.Message = "Path is not readable by DockTun (permission denied)."
		chk.Remediation = d.suggestRemediation(targetPath, isLXC, isNobody)
	} else if !chk.Writable {
		chk.Status = "error"
		if isNobody {
			chk.Message = "Unprivileged LXC permission conflict: path is owned by nobody:nogroup (65534) and write access is denied. Container root UID 0 (host UID 100000) cannot write."
			chk.Remediation = fmt.Sprintf("chown -R 100000:100000 %s && chmod -R 775 %s", targetPath, targetPath)
		} else {
			chk.Message = fmt.Sprintf("Path is read-only or permission denied for write: %v", err)
			chk.Remediation = d.suggestRemediation(targetPath, isLXC, false)
		}
	} else if isNobody {
		// Can write, but owned by 65534
		chk.Status = "warning"
		chk.Message = "Path is currently owned by nobody:nogroup (65534). Writes succeed, but Proxmox unprivileged host mapping (100000:100000) is recommended."
		chk.Remediation = fmt.Sprintf("chown -R 100000:100000 %s", targetPath)
	} else {
		chk.Status = "ok"
		chk.Message = "Read and write access verified successfully."
	}

	return chk
}

func (d *StorageDoctor) testRead(targetPath string, isDir bool) bool {
	if isDir {
		f, err := os.Open(targetPath)
		if err != nil {
			return false
		}
		defer f.Close()
		_, err = f.Readdirnames(1)
		return err == nil || errors.Is(err, io.EOF)
	}
	f, err := os.Open(targetPath)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 1)
	_, _ = f.Read(buf)
	return true
}

func (d *StorageDoctor) testWrite(targetPath string, isDir bool) (bool, error) {
	if isDir {
		probeName := fmt.Sprintf(".docktun_doctor_probe_%d", time.Now().UnixNano())
		probePath := filepath.Join(targetPath, probeName)
		f, err := os.OpenFile(probePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return false, err
		}
		_, _ = f.WriteString("probe")
		f.Close()
		_ = os.Remove(probePath)
		return true, nil
	}

	// Test write on existing regular file without modifying contents
	f, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false, err
	}
	f.Close()
	return true, nil
}

func (d *StorageDoctor) suggestRemediation(targetPath string, isLXC, isNobody bool) string {
	if isLXC || isNobody {
		return fmt.Sprintf("chown -R 100000:100000 %s && chmod -R u+rwX,g+rwX %s", targetPath, targetPath)
	}
	return fmt.Sprintf("chmod -R u+rwX %s", targetPath)
}

// extractComposeBindMounts searches YAML for host directory bind mounts.
func extractComposeBindMounts(yamlData []byte, projectPath string) []string {
	var root yaml.Node
	if err := yaml.Unmarshal(yamlData, &root); err != nil {
		return nil
	}

	mounts := []string{}
	var findVolumes func(node *yaml.Node)
	findVolumes = func(node *yaml.Node) {
		if node.Kind == yaml.DocumentNode {
			for _, child := range node.Content {
				findVolumes(child)
			}
		} else if node.Kind == yaml.MappingNode {
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i].Value
				val := node.Content[i+1]
				if key == "volumes" && val.Kind == yaml.SequenceNode {
					for _, item := range val.Content {
						if item.Kind == yaml.ScalarNode {
							// Format: /host/path:/container/path or ./relative:/container/path
							parts := strings.Split(item.Value, ":")
							if len(parts) >= 2 {
								src := parts[0]
								if strings.HasPrefix(src, "/") || strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") {
									abs := src
									if !filepath.IsAbs(abs) {
										abs = filepath.Clean(filepath.Join(projectPath, src))
									}
									mounts = append(mounts, abs)
								}
							}
						} else if item.Kind == yaml.MappingNode {
							// Long syntax: type: bind, source: /host/path
							var mType, mSrc string
							for j := 0; j < len(item.Content); j += 2 {
								mk := item.Content[j].Value
								mv := item.Content[j+1].Value
								if mk == "type" {
									mType = mv
								} else if mk == "source" {
									mSrc = mv
								}
							}
							if (mType == "bind" || mType == "") && mSrc != "" {
								abs := mSrc
								if !filepath.IsAbs(abs) {
									abs = filepath.Clean(filepath.Join(projectPath, mSrc))
								}
								mounts = append(mounts, abs)
							}
						}
					}
				} else {
					findVolumes(val)
				}
			}
		} else if node.Kind == yaml.SequenceNode {
			for _, item := range node.Content {
				findVolumes(item)
			}
		}
	}

	findVolumes(&root)
	return mounts
}
