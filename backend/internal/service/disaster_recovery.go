package service

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"docktun/internal/config"
	"docktun/internal/db"
)

const (
	maxArchiveReadLimit       int64 = 1 << 30      // 1 GB total decompressed read limit
	maxFileCount              int   = 10000        // Maximum number of entries in an archive
	maxSingleFileSize         int64 = 500 << 20    // 500 MB per file limit
	maxProjectSingleFileSize  int64 = 100 << 20    // 100 MB per project file limit
)

// DisasterRecoveryManifest contains archive metadata and checksums for verification.
type DisasterRecoveryManifest struct {
	Version   int                        `json:"version"`
	CreatedAt string                     `json:"created_at"`
	Generator string                     `json:"generator"`
	Files     []DisasterRecoveryFileMeta `json:"files"`
}

// DisasterRecoveryFileMeta describes an individual archived file.
type DisasterRecoveryFileMeta struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type archiveItem struct {
	sourcePath string
	tarPath    string
}

// DisasterRecoveryService handles full-system disaster recovery export and restore.
type DisasterRecoveryService struct {
	db             *db.DB
	cfg            *config.Config
	dbPath         string
	cloudflaredDir string
	projectsDir    string
	mu             sync.Mutex
}

// NewDisasterRecoveryService creates a new disaster recovery service instance.
func NewDisasterRecoveryService(database *db.DB, cfg *config.Config) *DisasterRecoveryService {
	dbPath := "./data/app.db"
	cfDir := "/etc/cloudflared"
	projDir := "/opt/projects"
	if cfg != nil {
		if cfg.DBPath != "" {
			dbPath = cfg.DBPath
		}
		if cfg.CloudflaredConfig != "" {
			cfDir = filepath.Dir(cfg.CloudflaredConfig)
		}
	}
	return &DisasterRecoveryService{
		db:             database,
		cfg:            cfg,
		dbPath:         dbPath,
		cloudflaredDir: cfDir,
		projectsDir:    projDir,
	}
}

// WithPaths overrides default paths for testing or customized host layouts.
func (s *DisasterRecoveryService) WithPaths(dbPath, cloudflaredDir, projectsDir string) *DisasterRecoveryService {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dbPath != "" {
		s.dbPath = dbPath
	}
	if cloudflaredDir != "" {
		s.cloudflaredDir = cloudflaredDir
	}
	if projectsDir != "" {
		s.projectsDir = projectsDir
	}
	return s
}

// ExportBackup safely packages app.db, cloudflared configuration, and project Compose files into a verified .tar.gz bundle.
func (s *DisasterRecoveryService) ExportBackup(w io.Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stagingDir, err := os.MkdirTemp("", "docktun-dr-export-*")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	var items []archiveItem

	// 1. Snapshot SQLite database
	snapDB, err := s.snapshotDatabase(stagingDir)
	if err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	items = append(items, archiveItem{sourcePath: snapDB, tarPath: "app.db"})

	// 2. Collect Cloudflared configuration files
	cfFilesFound := false
	if info, err := os.Stat(s.cloudflaredDir); err == nil && info.IsDir() {
		_ = filepath.Walk(s.cloudflaredDir, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(s.cloudflaredDir, p)
			if err != nil || rel == "." {
				return nil
			}
			tarName := "cloudflared/" + filepath.ToSlash(rel)
			items = append(items, archiveItem{sourcePath: p, tarPath: tarName})
			cfFilesFound = true
			return nil
		})
	}
	if !cfFilesFound && s.cfg != nil && s.cfg.CloudflaredConfig != "" {
		if fi, err := os.Stat(s.cfg.CloudflaredConfig); err == nil && fi.Mode().IsRegular() {
			items = append(items, archiveItem{sourcePath: s.cfg.CloudflaredConfig, tarPath: "cloudflared/config.yml"})
		}
	}

	// 3. Collect Project Compose and configuration files
	if info, err := os.Stat(s.projectsDir); err == nil && info.IsDir() {
		_ = filepath.Walk(s.projectsDir, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() {
				if fi.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
				return nil
			}
			if fi.Size() > maxProjectSingleFileSize {
				return nil
			}
			rel, err := filepath.Rel(s.projectsDir, p)
			if err != nil || rel == "." {
				return nil
			}
			tarName := "projects/" + filepath.ToSlash(rel)
			items = append(items, archiveItem{sourcePath: p, tarPath: tarName})
			return nil
		})
	}

	// 4. Generate manifest with SHA-256 hashes
	manifest := DisasterRecoveryManifest{
		Version:   1,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Generator: "docktun-disaster-recovery",
		Files:     make([]DisasterRecoveryFileMeta, 0, len(items)),
	}

	for _, item := range items {
		fi, err := os.Stat(item.sourcePath)
		if err != nil {
			return fmt.Errorf("stat file %s: %w", item.sourcePath, err)
		}
		hash, err := fileSHA256(item.sourcePath)
		if err != nil {
			return fmt.Errorf("hash file %s: %w", item.sourcePath, err)
		}
		manifest.Files = append(manifest.Files, DisasterRecoveryFileMeta{
			Path:   item.tarPath,
			Size:   fi.Size(),
			SHA256: hash,
		})
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	// 5. Write .tar.gz bundle
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	// Write manifest as first entry
	manifestHeader := &tar.Header{
		Name:     "manifest.json",
		Mode:     0644,
		Size:     int64(len(manifestBytes)),
		ModTime:  time.Now().UTC(),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(manifestHeader); err != nil {
		tw.Close()
		gw.Close()
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(manifestBytes); err != nil {
		tw.Close()
		gw.Close()
		return fmt.Errorf("write manifest content: %w", err)
	}

	// Write each archive item
	for _, item := range items {
		fi, err := os.Stat(item.sourcePath)
		if err != nil {
			tw.Close()
			gw.Close()
			return fmt.Errorf("stat item %s: %w", item.sourcePath, err)
		}
		hdr := &tar.Header{
			Name:     item.tarPath,
			Mode:     0644,
			Size:     fi.Size(),
			ModTime:  fi.ModTime().UTC(),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			tw.Close()
			gw.Close()
			return fmt.Errorf("write header %s: %w", item.tarPath, err)
		}
		file, err := os.Open(item.sourcePath)
		if err != nil {
			tw.Close()
			gw.Close()
			return fmt.Errorf("open item %s: %w", item.sourcePath, err)
		}
		_, copyErr := io.CopyN(tw, file, fi.Size())
		file.Close()
		if copyErr != nil {
			tw.Close()
			gw.Close()
			return fmt.Errorf("copy item content %s: %w", item.tarPath, copyErr)
		}
	}

	if err := tw.Close(); err != nil {
		gw.Close()
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}

	return nil
}

// RestoreBackup safely validates archive structure and restores database, cloudflared files, and project stacks.
func (s *DisasterRecoveryService) RestoreBackup(r io.Reader) error {
	stageDir, err := os.MkdirTemp("", "docktun-dr-restore-*")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)

	limitedReader := io.LimitReader(r, maxArchiveReadLimit)
	gz, err := gzip.NewReader(limitedReader)
	if err != nil {
		return fmt.Errorf("invalid gzip archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	fileCount := 0
	var totalBytes int64

	// Extract and validate tar entries to staging directory
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive entry: %w", err)
		}

		fileCount++
		if fileCount > maxFileCount {
			return fmt.Errorf("archive contains too many files (limit: %d)", maxFileCount)
		}

		// Clean and validate path to prevent Zip Slip / directory traversal
		cleanName := path.Clean(header.Name)
		if strings.HasPrefix(cleanName, "/") || strings.HasPrefix(cleanName, "\\") ||
			strings.HasPrefix(cleanName, "..") || strings.Contains(cleanName, "/../") ||
			strings.Contains(cleanName, "\\..\\") || strings.Contains(cleanName, ":") {
			return fmt.Errorf("illegal archive file path traversal: %s", header.Name)
		}

		destPath := filepath.Join(stageDir, filepath.FromSlash(cleanName))
		if !strings.HasPrefix(destPath, stageDir) {
			return fmt.Errorf("archive path escapes destination: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return fmt.Errorf("create directory %s: %w", destPath, err)
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > maxSingleFileSize {
				return fmt.Errorf("file %s size exceeds allowed limit (%d bytes)", cleanName, header.Size)
			}
			totalBytes += header.Size
			if totalBytes > maxArchiveReadLimit {
				return fmt.Errorf("total decompressed size exceeds maximum limit (%d bytes)", maxArchiveReadLimit)
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return fmt.Errorf("create parent directory for %s: %w", destPath, err)
			}
			outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("create extracted file %s: %w", destPath, err)
			}
			n, copyErr := io.CopyN(outFile, tr, header.Size)
			outFile.Close()
			if copyErr != nil || n != header.Size {
				return fmt.Errorf("failed to extract file %s: %v", destPath, copyErr)
			}
		default:
			return fmt.Errorf("unsupported or unsafe file type in archive: %v (%s)", header.Typeflag, header.Name)
		}
	}

	// Validate archive structure and integrity
	manifestPath := filepath.Join(stageDir, "manifest.json")
	if raw, err := os.ReadFile(manifestPath); err == nil {
		var manifest DisasterRecoveryManifest
		if err := json.Unmarshal(raw, &manifest); err == nil && len(manifest.Files) > 0 {
			for _, item := range manifest.Files {
				fPath := filepath.Join(stageDir, filepath.FromSlash(item.Path))
				fi, err := os.Stat(fPath)
				if err != nil {
					return fmt.Errorf("archive missing file declared in manifest: %s", item.Path)
				}
				if fi.Size() != item.Size {
					return fmt.Errorf("archive file size mismatch for %s", item.Path)
				}
				hash, err := fileSHA256(fPath)
				if err != nil {
					return fmt.Errorf("hash verified file %s: %w", item.Path, err)
				}
				if hash != item.SHA256 {
					return fmt.Errorf("archive file checksum mismatch for %s", item.Path)
				}
			}
		}
	}

	// Locate database file
	var stagedDB string
	for _, candidate := range []string{
		filepath.Join(stageDir, "app.db"),
		filepath.Join(stageDir, "database", "app.db"),
		filepath.Join(stageDir, "data", "app.db"),
	} {
		if fi, err := os.Stat(candidate); err == nil && fi.Mode().IsRegular() {
			stagedDB = candidate
			break
		}
	}
	if stagedDB == "" {
		return fmt.Errorf("invalid disaster recovery archive: app.db not found")
	}

	// Verify SQLite database integrity using PRAGMA quick_check
	checkDB, err := db.New(stagedDB)
	if err != nil {
		return fmt.Errorf("restored database cannot be opened: %w", err)
	}
	var checkResult string
	err = checkDB.QueryRow("PRAGMA quick_check").Scan(&checkResult)
	checkDB.Close()
	if err != nil || checkResult != "ok" {
		return fmt.Errorf("restored database failed integrity check: %s", checkResult)
	}

	// Safely restore components to their destination paths
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Restore Cloudflared files
	stagedCF := filepath.Join(stageDir, "cloudflared")
	if info, err := os.Stat(stagedCF); err == nil && info.IsDir() {
		if err := os.MkdirAll(s.cloudflaredDir, 0755); err != nil {
			return fmt.Errorf("create cloudflared directory: %w", err)
		}
		err = filepath.Walk(stagedCF, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(stagedCF, p)
			if err != nil || rel == "." {
				return nil
			}
			target := filepath.Join(s.cloudflaredDir, rel)
			if fi.IsDir() {
				return os.MkdirAll(target, 0755)
			}
			return copyFile(p, target)
		})
		if err != nil {
			return fmt.Errorf("restore cloudflared configuration: %w", err)
		}
		// Also restore single config file if explicitly configured
		stagedConfigYml := filepath.Join(stagedCF, "config.yml")
		if s.cfg != nil && s.cfg.CloudflaredConfig != "" {
			if fi, err := os.Stat(stagedConfigYml); err == nil && fi.Mode().IsRegular() {
				_ = os.MkdirAll(filepath.Dir(s.cfg.CloudflaredConfig), 0755)
				_ = copyFile(stagedConfigYml, s.cfg.CloudflaredConfig)
			}
		}
	}

	// 2. Restore Project stacks
	stagedProjects := filepath.Join(stageDir, "projects")
	if info, err := os.Stat(stagedProjects); err == nil && info.IsDir() {
		if err := os.MkdirAll(s.projectsDir, 0755); err != nil {
			return fmt.Errorf("create projects directory: %w", err)
		}
		err = filepath.Walk(stagedProjects, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(stagedProjects, p)
			if err != nil || rel == "." {
				return nil
			}
			target := filepath.Join(s.projectsDir, rel)
			if fi.IsDir() {
				return os.MkdirAll(target, 0755)
			}
			return copyFile(p, target)
		})
		if err != nil {
			return fmt.Errorf("restore project files: %w", err)
		}
	}

	// 3. Restore Database
	if s.db != nil {
		_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
	if err := os.MkdirAll(filepath.Dir(s.dbPath), 0755); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	_ = os.Remove(s.dbPath + "-wal")
	_ = os.Remove(s.dbPath + "-shm")
	if err := copyFile(stagedDB, s.dbPath); err != nil {
		return fmt.Errorf("restore database file: %w", err)
	}
	if s.db != nil {
		_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		_, _ = s.db.Exec("PRAGMA schema_version")
	}

	return nil
}

func (s *DisasterRecoveryService) snapshotDatabase(stagingDir string) (string, error) {
	snapPath := filepath.Join(stagingDir, "app.db")
	if s.db != nil {
		escaped := strings.ReplaceAll(snapPath, "'", "''")
		_, err := s.db.Exec("VACUUM INTO '" + escaped + "'")
		if err == nil {
			return snapPath, nil
		}
		_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
	if fi, err := os.Stat(s.dbPath); err == nil && fi.Mode().IsRegular() {
		if err := copyFile(s.dbPath, snapPath); err != nil {
			return "", fmt.Errorf("copy database: %w", err)
		}
		return snapPath, nil
	}
	return "", fmt.Errorf("database file not found at %s", s.dbPath)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
