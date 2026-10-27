package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"docktun/internal/config"
	"docktun/internal/db"
)

type StorageOverview struct {
	HostDisk        DiskSpace              `json:"host_disk"`
	DockerDF        []DockerDFItem         `json:"docker_df"`
	ReclaimableStr  string                 `json:"reclaimable_str"`
	ReclaimableBytes int64                 `json:"reclaimable_bytes"`
	Counts          StorageCounts          `json:"counts"`
	DockTunStorage  DockTunStorageInfo     `json:"docktun_storage"`
}

type DiskSpace struct {
	TotalStr   string  `json:"total_str"`
	UsedStr    string  `json:"used_str"`
	FreeStr    string  `json:"free_str"`
	TotalBytes int64   `json:"total_bytes"`
	UsedBytes  int64   `json:"used_bytes"`
	FreeBytes  int64   `json:"free_bytes"`
	Percent    float64 `json:"percent"`
}

type DockerDFItem struct {
	Type        string `json:"type"`
	TotalCount  int    `json:"total_count"`
	ActiveCount int    `json:"active_count"`
	Size        string `json:"size"`
	Reclaimable string `json:"reclaimable"`
}

type StorageCounts struct {
	TotalImages      int `json:"total_images"`
	UnusedImages     int `json:"unused_images"`
	DanglingImages   int `json:"dangling_images"`
	TotalContainers  int `json:"total_containers"`
	StoppedContainers int `json:"stopped_containers"`
	TotalVolumes     int `json:"total_volumes"`
	DanglingVolumes  int `json:"dangling_volumes"`
	BuildCacheCount  int `json:"build_cache_count"`
}

type DockTunStorageInfo struct {
	DBPath       string `json:"db_path"`
	DBSizeBytes  int64  `json:"db_size_bytes"`
	DBSizeStr    string `json:"db_size_str"`
	BackupsCount int    `json:"backups_count"`
	BackupsBytes int64  `json:"backups_bytes"`
	BackupsStr   string `json:"backups_str"`
	AuditCount   int    `json:"audit_count"`
}

type VolumeItem struct {
	Name        string   `json:"name"`
	Driver      string   `json:"driver"`
	Scope       string   `json:"scope"`
	Mountpoint  string   `json:"mountpoint"`
	CreatedAt   string   `json:"created_at"`
	Size        string   `json:"size"`
	InUse       bool     `json:"in_use"`
	Containers  []string `json:"containers"`
	IsAnonymous bool     `json:"is_anonymous"`
}

type ImageItem struct {
	ID           string   `json:"id"`
	Repository   string   `json:"repository"`
	Tag          string   `json:"tag"`
	CreatedSince string   `json:"created_since"`
	CreatedAt    string   `json:"created_at"`
	Size         string   `json:"size"`
	InUse        bool     `json:"in_use"`
	IsDangling   bool     `json:"is_dangling"`
	Containers   []string `json:"containers"`
}

type ContainerStorageItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Image      string `json:"image"`
	State      string `json:"state"`
	Status     string `json:"status"`
	SizeRW     string `json:"size_rw"`
	SizeRootFS string `json:"size_rootfs"`
}

type ReclaimRequest struct {
	PruneImagesDangling bool `json:"prune_images_dangling"`
	PruneImagesAll      bool `json:"prune_images_all"`
	PruneContainers     bool `json:"prune_containers"`
	PruneVolumes        bool `json:"prune_volumes"`
	PruneBuildCache     bool `json:"prune_build_cache"`
}

type ReclaimResult struct {
	Outputs []string `json:"outputs"`
	Message string   `json:"message"`
}

type StorageService struct {
	db  *db.DB
	cfg *config.Config
}

func NewStorageService(database *db.DB, cfg *config.Config) *StorageService {
	return &StorageService{
		db:  database,
		cfg: cfg,
	}
}

// -----------------------------------------------------------------------------
// Overview
// -----------------------------------------------------------------------------

func (s *StorageService) GetOverview() (*StorageOverview, error) {
	overview := &StorageOverview{}

	// 1. Host Disk Space
	overview.HostDisk = s.getHostDisk()

	// 2. Docker DF summary
	dfItems, totalReclaimBytes := s.getDockerDF()
	overview.DockerDF = dfItems
	overview.ReclaimableBytes = totalReclaimBytes
	overview.ReclaimableStr = formatBytes(totalReclaimBytes)

	// 3. Counts from Volume & Image scans
	vols, _ := s.ListVolumes()
	imgs, _ := s.ListImages()
	containers, _ := s.ListContainersStorage()

	overview.Counts.TotalVolumes = len(vols)
	for _, v := range vols {
		if !v.InUse {
			overview.Counts.DanglingVolumes++
		}
	}

	overview.Counts.TotalImages = len(imgs)
	for _, img := range imgs {
		if !img.InUse {
			overview.Counts.UnusedImages++
		}
		if img.IsDangling {
			overview.Counts.DanglingImages++
		}
	}

	overview.Counts.TotalContainers = len(containers)
	for _, c := range containers {
		if strings.ToLower(c.State) != "running" {
			overview.Counts.StoppedContainers++
		}
	}

	for _, df := range dfItems {
		if strings.Contains(strings.ToLower(df.Type), "build") {
			overview.Counts.BuildCacheCount = df.TotalCount
		}
	}

	// 4. DockTun Local Storage Footprint
	overview.DockTunStorage = s.getDockTunStorage()

	return overview, nil
}

func (s *StorageService) getHostDisk() DiskSpace {
	ds := DiskSpace{
		TotalStr: "N/A",
		UsedStr:  "N/A",
		FreeStr:  "N/A",
	}

	checkDir := "/app/data"
	if _, err := os.Stat(checkDir); err != nil {
		checkDir = "/"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// -P enforces POSIX single-line output format, avoiding line wraps on long mount names
	cmd := exec.CommandContext(ctx, "df", "-P", "-k", checkDir)
	out, err := cmd.Output()
	if err != nil {
		cmdRoot := exec.CommandContext(ctx, "df", "-P", "-k", "/")
		out, err = cmdRoot.Output()
	}

	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[len(lines)-1])
			if len(fields) >= 3 {
				var totalIdx, usedIdx, freeIdx int
				if _, parseErr := strconv.ParseInt(fields[0], 10, 64); parseErr == nil {
					// Long filesystem name wrapped onto previous line; fields[0] is total blocks
					totalIdx, usedIdx, freeIdx = 0, 1, 2
				} else if len(fields) >= 4 {
					// Standard layout: fields[0] is filesystem device name, fields[1] is total blocks
					totalIdx, usedIdx, freeIdx = 1, 2, 3
				}

				if freeIdx < len(fields) {
					totalK, _ := strconv.ParseInt(fields[totalIdx], 10, 64)
					usedK, _ := strconv.ParseInt(fields[usedIdx], 10, 64)
					freeK, _ := strconv.ParseInt(fields[freeIdx], 10, 64)

					ds.TotalBytes = totalK * 1024
					ds.UsedBytes = usedK * 1024
					ds.FreeBytes = freeK * 1024

					ds.TotalStr = formatBytes(ds.TotalBytes)
					ds.UsedStr = formatBytes(ds.UsedBytes)
					ds.FreeStr = formatBytes(ds.FreeBytes)

					if ds.TotalBytes > 0 {
						ds.Percent = float64(ds.UsedBytes) / float64(ds.TotalBytes) * 100.0
					}
				}
			}
		}
	}

	return ds
}

func (s *StorageService) getDockerDF() ([]DockerDFItem, int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "system", "df", "--format", "{{json .}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, 0
	}

	var items []DockerDFItem
	var totalReclaimBytes int64

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(l), &raw); err != nil {
			continue
		}

		typeStr, _ := raw["Type"].(string)
		totalCountStr, _ := raw["TotalCount"].(string)
		activeCountStr, _ := raw["Active"].(string)
		sizeStr, _ := raw["Size"].(string)
		reclaimStr, _ := raw["Reclaimable"].(string)

		tot, _ := strconv.Atoi(totalCountStr)
		act, _ := strconv.Atoi(activeCountStr)

		recBytes := parseByteSize(reclaimStr)
		totalReclaimBytes += recBytes

		items = append(items, DockerDFItem{
			Type:        typeStr,
			TotalCount:  tot,
			ActiveCount: act,
			Size:        sizeStr,
			Reclaimable: reclaimStr,
		})
	}

	return items, totalReclaimBytes
}

func (s *StorageService) getDockTunStorage() DockTunStorageInfo {
	info := DockTunStorageInfo{
		DBPath: s.cfg.DBPath,
	}

	// DB file size
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		info.DBSizeBytes = fi.Size()
		info.DBSizeStr = formatBytes(fi.Size())
	} else {
		info.DBSizeStr = "0 B"
	}

	// Backups folder
	backupsDir := filepath.Join(filepath.Dir(s.cfg.CloudflaredConfig), "backups")
	if entries, err := os.ReadDir(backupsDir); err == nil {
		var totalB int64
		var count int
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".yml") {
				count++
				if fi, err := e.Info(); err == nil {
					totalB += fi.Size()
				}
			}
		}
		info.BackupsCount = count
		info.BackupsBytes = totalB
		info.BackupsStr = formatBytes(totalB)
	} else {
		info.BackupsStr = "0 B"
	}

	// Audit count
	_ = s.db.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&info.AuditCount)

	return info
}

// -----------------------------------------------------------------------------
// Volumes
// -----------------------------------------------------------------------------

func (s *StorageService) ListVolumes() ([]VolumeItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Build map of volume -> containers and detect dangling volumes
	volToContainers := make(map[string][]string)
	danglingVols := make(map[string]bool)

	// Use Docker's native ground truth for dangling volumes
	dangCmd := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "-f", "dangling=true")
	if dangOut, err := dangCmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(dangOut)), "\n") {
			vName := strings.TrimSpace(line)
			if vName != "" {
				danglingVols[vName] = true
			}
		}
	}

	// Inspect all containers to extract exact volume names attached
	idsCmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.ID}}")
	if idsOut, err := idsCmd.Output(); err == nil {
		cIDs := strings.Fields(strings.TrimSpace(string(idsOut)))
		if len(cIDs) > 0 {
			inspectArgs := append([]string{"inspect", "--format", "{{.Name}}\t{{range .Mounts}}{{if eq .Type \"volume\"}}{{.Name}},{{else if .Name}}{{.Name}},{{end}}{{end}}"}, cIDs...)
			if inspOut, err := exec.CommandContext(ctx, "docker", inspectArgs...).Output(); err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(inspOut)), "\n") {
					parts := strings.Split(line, "\t")
					if len(parts) >= 2 {
						cName := strings.TrimPrefix(strings.TrimSpace(parts[0]), "/")
						vols := strings.Split(parts[1], ",")
						for _, v := range vols {
							v = strings.TrimSpace(v)
							if v != "" {
								volToContainers[v] = append(volToContainers[v], cName)
							}
						}
					}
				}
			}
		}
	}

	// 2. Volume sizes from docker system df -v
	volSizes := make(map[string]string)
	dfCmd := exec.CommandContext(ctx, "docker", "system", "df", "-v", "--format", "{{json .}}")
	if dfOut, err := dfCmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(dfOut)), "\n") {
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(line), &obj); err == nil {
				if vName, ok := obj["VolumeName"].(string); ok && vName != "" {
					if sz, ok := obj["Size"].(string); ok {
						volSizes[vName] = sz
					}
				}
			}
		}
	}

	// 3. List volumes
	lsCmd := exec.CommandContext(ctx, "docker", "volume", "ls", "--format", "{{json .}}")
	out, err := lsCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker volume ls failed: %w", err)
	}

	hexRegex := regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

	var volumes []VolumeItem
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(l), &raw); err != nil {
			continue
		}

		name, _ := raw["Name"].(string)
		driver, _ := raw["Driver"].(string)
		scope, _ := raw["Scope"].(string)
		mountpoint, _ := raw["Mountpoint"].(string)
		createdAt, _ := raw["CreatedAt"].(string)
		sz, _ := raw["Size"].(string)

		if sz == "" || sz == "N/A" {
			if s, ok := volSizes[name]; ok {
				sz = s
			} else {
				sz = "N/A"
			}
		}

		attached := volToContainers[name]
		isAnon := hexRegex.MatchString(name)

		isDang := danglingVols[name]
		inUse := !isDang || len(attached) > 0

		volumes = append(volumes, VolumeItem{
			Name:        name,
			Driver:      driver,
			Scope:       scope,
			Mountpoint:  mountpoint,
			CreatedAt:   createdAt,
			Size:        sz,
			InUse:       inUse,
			Containers:  attached,
			IsAnonymous: isAnon,
		})
	}

	return volumes, nil
}

func (s *StorageService) DeleteVolume(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("volume name required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "volume", "rm", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("delete volume failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *StorageService) PruneVolumes() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "volume", "prune", "-f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("prune volumes failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// -----------------------------------------------------------------------------
// Images
// -----------------------------------------------------------------------------

func (s *StorageService) ListImages() ([]ImageItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Build map of image -> container names
	imgToContainers := make(map[string][]string)
	psCmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.Image}}\t{{.Names}}")
	if psOut, err := psCmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(psOut)), "\n") {
			parts := strings.Split(line, "\t")
			if len(parts) >= 2 {
				imgName := strings.TrimSpace(parts[0])
				cName := strings.TrimSpace(parts[1])
				imgToContainers[imgName] = append(imgToContainers[imgName], cName)
			}
		}
	}

	// 2. List all images
	cmd := exec.CommandContext(ctx, "docker", "images", "-a", "--format", "{{json .}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker images failed: %w", err)
	}

	var images []ImageItem
	seen := make(map[string]bool)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(l), &raw); err != nil {
			continue
		}

		id, _ := raw["ID"].(string)
		repo, _ := raw["Repository"].(string)
		tag, _ := raw["Tag"].(string)
		size, _ := raw["Size"].(string)
		createdSince, _ := raw["CreatedSince"].(string)
		createdAt, _ := raw["CreatedAt"].(string)

		uniqueKey := fmt.Sprintf("%s:%s:%s", id, repo, tag)
		if seen[uniqueKey] {
			continue
		}
		seen[uniqueKey] = true

		isDangling := repo == "<none>" || tag == "<none>"

		fullRef := repo
		if tag != "" && tag != "<none>" {
			fullRef = repo + ":" + tag
		}

		attached := imgToContainers[fullRef]
		if len(attached) == 0 {
			attached = imgToContainers[id]
		}
		if len(attached) == 0 && repo != "" {
			attached = imgToContainers[repo]
		}

		images = append(images, ImageItem{
			ID:           id,
			Repository:   repo,
			Tag:          tag,
			CreatedSince: createdSince,
			CreatedAt:    createdAt,
			Size:         size,
			InUse:        len(attached) > 0,
			IsDangling:   isDangling,
			Containers:   attached,
		})
	}

	return images, nil
}

func (s *StorageService) DeleteImage(id string, force bool) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("image ID required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	args := []string{"rmi"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, id)

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("delete image failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *StorageService) PruneImages(all bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	args := []string{"image", "prune", "-f"}
	if all {
		args = append(args, "-a")
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("prune images failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// -----------------------------------------------------------------------------
// Containers Storage
// -----------------------------------------------------------------------------

func (s *StorageService) ListContainersStorage() ([]ContainerStorageItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--size", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.State}}\t{{.Status}}\t{{.Size}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps size failed: %w", err)
	}

	var items []ContainerStorageItem
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		parts := strings.Split(l, "\t")
		if len(parts) < 5 {
			continue
		}

		id := strings.TrimSpace(parts[0])
		name := strings.TrimPrefix(strings.TrimSpace(parts[1]), "/")
		img := strings.TrimSpace(parts[2])
		state := strings.TrimSpace(parts[3])
		status := strings.TrimSpace(parts[4])

		sizeRW := "0 B"
		sizeRootFS := "0 B"

		if len(parts) >= 6 {
			rawSize := strings.TrimSpace(parts[5])
			// Example rawSize: "12kB (virtual 1.1GB)"
			if idx := strings.Index(rawSize, "(virtual"); idx != -1 {
				sizeRW = strings.TrimSpace(rawSize[:idx])
				sizeRootFS = strings.TrimSuffix(strings.TrimSpace(rawSize[idx+8:]), ")")
			} else {
				sizeRW = rawSize
			}
		}

		items = append(items, ContainerStorageItem{
			ID:         id,
			Name:       name,
			Image:      img,
			State:      state,
			Status:     status,
			SizeRW:     sizeRW,
			SizeRootFS: sizeRootFS,
		})
	}

	return items, nil
}

func (s *StorageService) PruneContainers() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	psCmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "status=exited", "--filter", "status=created", "--format", "{{.ID}}\t{{.Names}}\t{{.Labels}}")
	out, err := psCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to list stopped containers: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var removed []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			cID := strings.TrimSpace(parts[0])
			cName := strings.TrimPrefix(strings.TrimSpace(parts[1]), "/")
			labels := ""
			if len(parts) >= 3 {
				labels = parts[2]
			}

			// Strictly protect DockTun infrastructure containers (e.g. docktun-wireguard, docktun-cloudflared, docktun-tailscale)
			if strings.HasPrefix(cName, "docktun") {
				continue
			}
			// Strictly protect Docker Compose services
			if strings.Contains(labels, "com.docker.compose.project") {
				continue
			}

			if _, rmErr := exec.CommandContext(ctx, "docker", "rm", cID).CombinedOutput(); rmErr == nil {
				removed = append(removed, cName)
			}
		}
	}

	if len(removed) == 0 {
		return "0 containers removed (all managed services, WireGuard, and Compose stacks safely preserved)", nil
	}
	return fmt.Sprintf("Removed %d unmanaged stopped containers: %s", len(removed), strings.Join(removed, ", ")), nil
}

// -----------------------------------------------------------------------------
// Build Cache
// -----------------------------------------------------------------------------

func (s *StorageService) PruneBuildCache(all bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	args := []string{"builder", "prune", "-f"}
	if all {
		args = append(args, "-a")
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("prune build cache failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// -----------------------------------------------------------------------------
// Granular Reclaim Batch
// -----------------------------------------------------------------------------

func (s *StorageService) ReclaimBatch(req ReclaimRequest) (*ReclaimResult, error) {
	var outputs []string

	if req.PruneContainers {
		if out, err := s.PruneContainers(); err == nil {
			outputs = append(outputs, "[CONTAINERS]\n"+out)
		} else {
			outputs = append(outputs, "[CONTAINERS ERROR] "+err.Error())
		}
	}

	if req.PruneImagesAll {
		if out, err := s.PruneImages(true); err == nil {
			outputs = append(outputs, "[IMAGES (ALL UNUSED)]\n"+out)
		} else {
			outputs = append(outputs, "[IMAGES ERROR] "+err.Error())
		}
	} else if req.PruneImagesDangling {
		if out, err := s.PruneImages(false); err == nil {
			outputs = append(outputs, "[IMAGES (DANGLING)]\n"+out)
		} else {
			outputs = append(outputs, "[IMAGES ERROR] "+err.Error())
		}
	}

	if req.PruneVolumes {
		if out, err := s.PruneVolumes(); err == nil {
			outputs = append(outputs, "[VOLUMES]\n"+out)
		} else {
			outputs = append(outputs, "[VOLUMES ERROR] "+err.Error())
		}
	}

	if req.PruneBuildCache {
		if out, err := s.PruneBuildCache(true); err == nil {
			outputs = append(outputs, "[BUILD CACHE]\n"+out)
		} else {
			outputs = append(outputs, "[BUILD CACHE ERROR] "+err.Error())
		}
	}

	return &ReclaimResult{
		Outputs: outputs,
		Message: "Selected resources pruned successfully",
	}, nil
}

// -----------------------------------------------------------------------------
// SQLite Database Vacuum
// -----------------------------------------------------------------------------

func (s *StorageService) VacuumDB() (map[string]interface{}, error) {
	var oldSize int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		oldSize = fi.Size()
	}

	if _, err := s.db.Exec("VACUUM"); err != nil {
		return nil, fmt.Errorf("sqlite vacuum failed: %w", err)
	}

	var newSize int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		newSize = fi.Size()
	}

	freed := oldSize - newSize
	if freed < 0 {
		freed = 0
	}

	return map[string]interface{}{
		"old_size_bytes": oldSize,
		"old_size_str":   formatBytes(oldSize),
		"new_size_bytes": newSize,
		"new_size_str":   formatBytes(newSize),
		"freed_bytes":    freed,
		"freed_str":      formatBytes(freed),
		"message":        fmt.Sprintf("SQLite database vacuumed. Reclaimed %s.", formatBytes(freed)),
	}, nil
}

// -----------------------------------------------------------------------------
// Byte Parser Helpers
// -----------------------------------------------------------------------------

func parseByteSize(str string) int64 {
	str = strings.TrimSpace(str)
	if str == "" || str == "0" || str == "N/A" {
		return 0
	}

	// Match e.g. "44.26GB (80%)" -> extract "44.26GB"
	parts := strings.Fields(str)
	if len(parts) > 0 {
		str = parts[0]
	}

	re := regexp.MustCompile(`^([\d.]+)\s*([a-zA-Z]+)`)
	matches := re.FindStringSubmatch(str)
	if len(matches) < 3 {
		return 0
	}

	val, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0
	}

	unit := strings.ToUpper(matches[2])
	var multiplier float64 = 1

	switch {
	case strings.HasPrefix(unit, "TB"):
		multiplier = 1024 * 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "GB"):
		multiplier = 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "MB"):
		multiplier = 1024 * 1024
	case strings.HasPrefix(unit, "KB"):
		multiplier = 1024
	case strings.HasPrefix(unit, "B"):
		multiplier = 1
	}

	return int64(val * multiplier)
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
		tb = gb * 1024
	)

	switch {
	case bytes >= tb:
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(tb))
	case bytes >= gb:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
