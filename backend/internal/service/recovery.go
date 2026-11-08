package service

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"database/sql"
	"docktun/internal/config"
	"docktun/internal/db"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const recoveryMaximum int64 = 16 << 30

var recoveryVolumeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var recoveryIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

type DatabaseBackupSelection struct {
	Container string `json:"container"`
	Engine    string `json:"engine"`
	Database  string `json:"database"`
	User      string `json:"user"`
}
type RecoveryRequest struct {
	ActorID        int                       `json:"-"`
	Passphrase     string                    `json:"passphrase"`
	Instance       bool                      `json:"instance"`
	Projects       []int                     `json:"projects"`
	Volumes        []string                  `json:"volumes"`
	Databases      []DatabaseBackupSelection `json:"databases"`
	StopContainers []string                  `json:"stop_containers"`
}
type RecoveryEntry struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	File     string `json:"file"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Engine   string `json:"engine,omitempty"`
	Database string `json:"database,omitempty"`
	User     string `json:"user,omitempty"`
	Image    string `json:"image,omitempty"`
	Major    string `json:"major,omitempty"`
}
type RecoveryManifest struct {
	Version   int             `json:"version"`
	CreatedAt int64           `json:"created_at"`
	Entries   []RecoveryEntry `json:"entries"`
	Notes     []string        `json:"notes"`
}
type RecoveryBackup struct {
	ID                string        `json:"id"`
	OperationID       string        `json:"operation_id"`
	Phase             string        `json:"phase"`
	CreatedAt         int64         `json:"created_at"`
	Size              int64         `json:"size"`
	SHA256            string        `json:"sha256"`
	Summary           string        `json:"summary"`
	IntegrityVerified sql.NullInt64 `json:"integrity_verified_at"`
	Restored          sql.NullInt64 `json:"restored_at"`
	RemoteUploaded    sql.NullInt64 `json:"remote_uploaded_at"`
	Error             string        `json:"error"`
}
type RecoveryService struct {
	DB           *db.DB
	Config       *config.Config
	Operations   *OperationStore
	remoteClient *http.Client
	Directory    string
}

func NewRecovery(database *db.DB, cfg *config.Config, operations *OperationStore) *RecoveryService {
	return &RecoveryService{DB: database, Config: cfg, Operations: operations, Directory: filepath.Join(filepath.Dir(cfg.DBPath), "recovery")}
}
func (s *RecoveryService) backupPath(id string) (string, error) {
	parsed, e := uuid.Parse(id)
	if e != nil || parsed.String() != id {
		return "", fmt.Errorf("invalid recovery archive")
	}
	return filepath.Join(s.Directory, "archives", id+".dtrec"), nil
}
func (s *RecoveryService) List() ([]RecoveryBackup, error) {
	rows, err := s.DB.Query(`SELECT id,operation_id,phase,created_at,size,sha256,summary,integrity_verified_at,restored_at,remote_uploaded_at,last_error FROM recovery_backups ORDER BY created_at DESC,id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RecoveryBackup{}
	for rows.Next() {
		var item RecoveryBackup
		if err = rows.Scan(&item.ID, &item.OperationID, &item.Phase, &item.CreatedAt, &item.Size, &item.SHA256, &item.Summary, &item.IntegrityVerified, &item.Restored, &item.RemoteUploaded, &item.Error); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *RecoveryService) StartBackup(request RecoveryRequest) (RecoveryBackup, error) {
	if len(request.Passphrase) < 12 || len(request.Passphrase) > 1024 {
		return RecoveryBackup{}, fmt.Errorf("use a separately held recovery passphrase of 12–1024 bytes")
	}
	if !request.Instance && len(request.Projects)+len(request.Volumes)+len(request.Databases) == 0 {
		return RecoveryBackup{}, fmt.Errorf("select at least one backup level")
	}
	if len(request.Projects)+len(request.Volumes) > 100 || len(request.Databases) > 20 || len(request.StopContainers) > 100 {
		return RecoveryBackup{}, fmt.Errorf("too many recovery selections")
	}
	seen := map[string]bool{}
	for _, volume := range request.Volumes {
		key := "volume:" + volume
		if seen[key] || !recoveryVolumeIdentifier.MatchString(volume) {
			return RecoveryBackup{}, fmt.Errorf("invalid or duplicate volume selection")
		}
		seen[key] = true
	}
	for _, id := range request.Projects {
		key := fmt.Sprintf("project:%d", id)
		if id < 1 || seen[key] {
			return RecoveryBackup{}, fmt.Errorf("invalid or duplicate project selection")
		}
		seen[key] = true
	}
	job, err := s.Operations.Begin("recovery_backup", "maintenance:recovery", map[string]any{"instance": request.Instance, "project_count": len(request.Projects), "volume_count": len(request.Volumes), "database_count": len(request.Databases)})
	if err != nil {
		return RecoveryBackup{}, err
	}
	item := RecoveryBackup{ID: uuid.NewString(), OperationID: job.ID, Phase: "creating", CreatedAt: time.Now().Unix()}
	summary, _ := json.Marshal(map[string]any{"instance": request.Instance, "projects": len(request.Projects), "volumes": len(request.Volumes), "databases": len(request.Databases)})
	if _, err = s.DB.Exec(`INSERT INTO recovery_backups(id,operation_id,phase,created_at,summary) VALUES(?,?,?,?,?)`, item.ID, job.ID, item.Phase, item.CreatedAt, string(summary)); err != nil {
		s.Operations.Finish(job.ID, err)
		return item, err
	}
	go func() {
		err := s.backup(item, request)
		request.Passphrase = ""
		if err != nil {
			_, _ = s.DB.Exec(`UPDATE recovery_backups SET phase='failed',last_error='Backup failed; inspect selected resources, permissions, available disk space and operation state' WHERE id=?`, item.ID)
		}
		_ = s.Operations.Finish(job.ID, err)
		if err == nil {
			s.afterBackup(item.ID, request.ActorID)
		}
	}()
	return item, nil
}
func (s *RecoveryService) backup(item RecoveryBackup, request RecoveryRequest) (result error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := os.MkdirAll(filepath.Join(s.Directory, "archives"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.Directory, "tmp"), 0700); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(filepath.Join(s.Directory, "tmp"), "backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	stopped := []string{}
	defer func() {
		restore, done := context.WithTimeout(context.Background(), 3*time.Minute)
		defer done()
		for _, id := range stopped {
			if _, e := updateDocker(restore, "start", id); e != nil {
				result = &operationRecoveryRequired{fmt.Errorf("a selected container could not be resumed; inspect operation resources before retrying")}
			}
		}
	}()
	selectedStop := map[string]bool{}
	core, err := selfContainer(ctx)
	if err != nil {
		return err
	}
	for _, id := range request.StopContainers {
		container, e := inspectUpdateContainer(ctx, id)
		if e != nil {
			return e
		}
		if container.ID == core.ID {
			return fmt.Errorf("cannot stop DockTun while exporting its backup")
		}
		if container.State.Paused {
			return fmt.Errorf("resume paused containers before quiesced volume backup")
		}
		selectedStop[container.ID] = true
	}
	databaseSelections := map[string]bool{}
	for _, selection := range request.Databases {
		container, e := inspectUpdateContainer(ctx, selection.Container)
		if e != nil {
			return e
		}
		key := container.ID + "/" + selection.Database
		if databaseSelections[key] {
			return fmt.Errorf("duplicate database selection")
		}
		databaseSelections[key] = true
		if selectedStop[container.ID] {
			return fmt.Errorf("do not stop a database selected for a logical export; use either quiesced volume backup or database export")
		}
	}
	// Every live volume writer must be explicitly selected for quiescing.
	for _, volume := range request.Volumes {
		if !recoveryVolumeIdentifier.MatchString(volume) {
			return fmt.Errorf("invalid volume selection")
		}
		if _, err = updateDocker(ctx, "volume", "inspect", volume); err != nil {
			return err
		}
		raw, e := updateDocker(ctx, "ps", "-q", "--filter", "volume="+volume)
		if e != nil {
			return e
		}
		for _, id := range strings.Fields(string(raw)) {
			container, e := inspectUpdateContainer(ctx, id)
			if e != nil {
				return e
			}
			if !selectedStop[container.ID] {
				return fmt.Errorf("running volume writer must be selected for stopping, or use a database-consistent export")
			}
		}
	}
	resources := []string{}
	for id := range selectedStop {
		container, e := inspectUpdateContainer(ctx, id)
		if e != nil {
			return e
		}
		if container.State.Running {
			resources = append(resources, "originally-running:"+id)
		}
	}
	if err = s.Operations.MergeSnapshot(item.OperationID, map[string]any{"resources": resources}); err != nil {
		return err
	}
	for id := range selectedStop {
		container, e := inspectUpdateContainer(ctx, id)
		if e != nil {
			return e
		}
		if container.State.Running {
			if _, e = updateDocker(ctx, "stop", "--time", "30", id); e != nil {
				return e
			}
			stopped = append(stopped, id)
		}
	}
	manifest := RecoveryManifest{Version: 1, CreatedAt: time.Now().Unix(), Entries: []RecoveryEntry{}, Notes: []string{"Keep the recovery passphrase separately from this host.", "Database exports are consistent snapshots; arbitrary live volume copies are not allowed.", "Restore volume and instance data only to new volumes; start providers explicitly after reviewing host settings."}}
	add := func(kind, name, file, engine, database, user, image string) error {
		info, e := os.Stat(file)
		if e != nil {
			return e
		}
		if info.Size() > recoveryMaximum {
			return fmt.Errorf("individual backup exceeds limit")
		}
		f, e := os.Open(file)
		if e != nil {
			return e
		}
		hash := sha256.New()
		_, e = io.Copy(hash, f)
		f.Close()
		if e != nil {
			return e
		}
		manifest.Entries = append(manifest.Entries, RecoveryEntry{Kind: kind, Name: name, File: fmt.Sprintf("objects/%03d", len(manifest.Entries)), Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)), Engine: engine, Database: database, User: user, Image: image})
		return nil
	}
	files := []string{}
	if request.Instance {
		file := filepath.Join(temp, "instance.tar")
		if err = s.exportInstance(file); err != nil {
			return err
		}
		sourceImage, _ := core.Config["Image"].(string)
		if info, e := inspectUpdateImage(ctx, core.Image); e == nil && len(info.RepoDigests) > 0 {
			sourceImage = info.RepoDigests[0]
		}
		if err = add("instance", "docktun", file, "", "", "", sourceImage); err != nil {
			return err
		}
		files = append(files, file)
	}
	for _, id := range request.Projects {
		project, e := NewProjects(s.DB, s.Config.SelfPath).GetByID(id)
		if e != nil {
			return e
		}
		file := filepath.Join(temp, uuid.NewString()+".project")
		f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = NewProjects(s.DB, s.Config.SelfPath).Backup(id, f)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if e = add("project", project.Name, file, "", "", "", ""); e != nil {
			return e
		}
		files = append(files, file)
	}
	for _, volume := range request.Volumes {
		file := filepath.Join(temp, uuid.NewString()+".volume")
		f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		live, e := updateDocker(ctx, "ps", "-q", "--filter", "volume="+volume)
		if e != nil || strings.TrimSpace(string(live)) != "" {
			f.Close()
			return fmt.Errorf("volume gained a live writer after quiescing; backup refused")
		}
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--read-only", "--mount", "type=volume,source="+volume+",target=/backup-volume,readonly", "--entrypoint", "/bin/tar", core.Image, "-C", "/backup-volume", "-cf", "-", ".")
		e = streamRecoveryCommand(cmd, f, recoveryMaximum)
		f.Close()
		if e != nil {
			return e
		}
		if e = add("volume", volume, file, "", "", "", ""); e != nil {
			return e
		}
		files = append(files, file)
	}
	for _, selection := range request.Databases {
		file := filepath.Join(temp, uuid.NewString()+".database")
		image, e := s.exportDatabase(ctx, selection, file)
		if e != nil {
			return e
		}
		if e = add("database", selection.Container, file, selection.Engine, selection.Database, selection.User, image); e != nil {
			return e
		}
		source, e := inspectUpdateContainer(ctx, selection.Container)
		if e != nil {
			return e
		}
		engine, major, e := databaseContainerVersion(ctx, source)
		if e != nil {
			return e
		}
		if engine != selection.Engine {
			return fmt.Errorf("selected database engine does not match the running container")
		}
		manifest.Entries[len(manifest.Entries)-1].Major = major
		files = append(files, file)
	}
	// Resume selected services as soon as snapshots have been captured; encryption
	// and upload do not extend their downtime.
	for _, id := range stopped {
		if _, err = updateDocker(ctx, "start", id); err != nil {
			return err
		}
	}
	stopped = nil
	archive := filepath.Join(temp, "archive.tar")
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(out)
	raw, _ := json.Marshal(manifest)
	if err = writeBackupFile(tw, "manifest.json", raw); err != nil {
		out.Close()
		return err
	}
	var total int64
	for i, entry := range manifest.Entries {
		total += entry.Size
		if total > recoveryMaximum {
			out.Close()
			return fmt.Errorf("combined archive exceeds 16 GiB limit")
		}
		f, e := os.Open(files[i])
		if e != nil {
			out.Close()
			return e
		}
		e = tw.WriteHeader(&tar.Header{Name: entry.File, Mode: 0600, Size: entry.Size})
		if e == nil {
			_, e = io.Copy(tw, f)
		}
		f.Close()
		if e != nil {
			out.Close()
			return e
		}
	}
	if err = tw.Close(); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	_ = s.Operations.Phase(item.OperationID, "running", "Snapshots captured; encrypting archive")
	final, _ := s.backupPath(item.ID)
	encrypted, err := os.OpenFile(final+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(final + ".tmp")
	plain, err := os.Open(archive)
	if err != nil {
		encrypted.Close()
		return err
	}
	err = EncryptRecovery(encrypted, plain, request.Passphrase)
	if err == nil {
		err = encrypted.Sync()
	}
	plain.Close()
	closeErr := encrypted.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(final+".tmp", final); err != nil {
		return err
	}
	file, err := os.Open(final)
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	file.Close()
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE recovery_backups SET phase='complete',size=?,sha256=? WHERE id=?`, size, hex.EncodeToString(hash.Sum(nil)), item.ID)
	return err
}
func streamRecoveryCommand(cmd *exec.Cmd, out io.Writer, maximum int64) error {
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(pipe, maximum+1))
	if copyErr != nil || n > maximum {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("backup command failed or exceeded size limit")
	}
	if err = cmd.Wait(); err != nil {
		return fmt.Errorf("backup/restore command failed; verify selected engine, account and permissions")
	}
	return nil
}
func (s *RecoveryService) exportInstance(destination string) error {
	snapshot := destination + ".db"
	defer os.Remove(snapshot)
	if _, err := s.DB.Exec(`VACUUM INTO '` + strings.ReplaceAll(snapshot, "'", "''") + `'`); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	tw := tar.NewWriter(out)
	defer tw.Close()
	if err = writeBackupFile(tw, "jwt.secret", []byte(s.Config.JWTSecret)); err != nil {
		return err
	}
	if err = writeBackupFile(tw, "setup.token", []byte(s.Config.SetupToken)); err != nil {
		return err
	}
	// The new host must not inherit active leases or assume local archive files
	// exist. History remains visible, but no old operation is auto-replayed.
	snapshotDB, e := db.New(snapshot)
	if e != nil {
		return e
	}
	_, e = snapshotDB.Exec(`DELETE FROM operation_locks; DELETE FROM recovery_download_tickets; DELETE FROM auth_sessions; UPDATE operations SET phase='restored_history',message='Historical operation from recovered host' WHERE phase IN ('preparing','running','checking','restoring'); UPDATE recovery_backups SET phase='not_local',last_error='Archive file is external to this instance backup'; INSERT INTO system_settings(key,value) VALUES('recovery_review_required','true') ON CONFLICT(key) DO UPDATE SET value='true'`)
	if e == nil {
		_, e = snapshotDB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	closeErr := snapshotDB.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	selected := map[string]string{"app.db": snapshot, "notifications.json": filepath.Join(filepath.Dir(s.Config.DBPath), "notifications.json"), "mesh.json": s.Config.MeshConfigPath, "cloudflared/config.yml": s.Config.CloudflaredConfig, "cloudflared/credentials.json": filepath.Join(filepath.Dir(s.Config.CloudflaredConfig), "credentials.json")}
	for name, file := range selected {
		info, e := os.Lstat(file)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("instance backup source must be a regular file")
		}
		if info.Size() > 512<<20 {
			return fmt.Errorf("instance configuration exceeds backup bound")
		}
		f, e := os.Open(file)
		if e != nil {
			return e
		}
		e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size()})
		if e == nil {
			_, e = io.Copy(tw, f)
		}
		f.Close()
		if e != nil {
			return e
		}
	}
	return tw.Close()
}
func (s *RecoveryService) exportDatabase(ctx context.Context, selection DatabaseBackupSelection, destination string) (string, error) {
	if !recoveryIdentifier.MatchString(selection.Database) || !recoveryIdentifier.MatchString(selection.User) {
		return "", fmt.Errorf("choose explicit safe database and account names")
	}
	container, err := inspectUpdateContainer(ctx, selection.Container)
	if err != nil {
		return "", err
	}
	if !container.State.Running {
		return "", fmt.Errorf("database container must be running for consistent export")
	}
	args := []string{"exec", container.ID, "sh", "-c"}
	switch selection.Engine {
	case "postgres":
		args = append(args, `if [ -z "${PGPASSWORD:-}" ]; then if [ -n "${POSTGRES_PASSWORD_FILE:-}" ]; then PGPASSWORD=$(cat "$POSTGRES_PASSWORD_FILE") || exit 1; else PGPASSWORD=${POSTGRES_PASSWORD:-}; fi; fi; export PGPASSWORD; exec pg_dump --format=custom --no-owner --no-acl --username="$1" --dbname="$2"`, "recovery", selection.User, selection.Database)
	case "mysql", "mariadb":
		binary := "mysqldump"
		if selection.Engine == "mariadb" {
			binary = "mariadb-dump"
		}
		args = append(args, `if [ -z "${MYSQL_PWD:-}" ]; then if [ "$2" = root ]; then password=${MYSQL_ROOT_PASSWORD:-${MARIADB_ROOT_PASSWORD:-}}; password_file=${MYSQL_ROOT_PASSWORD_FILE:-${MARIADB_ROOT_PASSWORD_FILE:-}}; else password=${MYSQL_PASSWORD:-${MARIADB_PASSWORD:-}}; password_file=${MYSQL_PASSWORD_FILE:-${MARIADB_PASSWORD_FILE:-}}; fi; if [ -n "$password_file" ]; then password=$(cat "$password_file") || exit 1; fi; MYSQL_PWD=$password; fi; export MYSQL_PWD; exec "$1" --lock-all-tables --routines --events --triggers --user="$2" "$3"`, "recovery", binary, selection.User, selection.Database)
	default:
		return "", fmt.Errorf("supported consistent exports: postgres, mysql, mariadb")
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	err = streamRecoveryCommand(exec.CommandContext(ctx, "docker", args...), file, recoveryMaximum)
	return container.Image, err
}
func (s *RecoveryService) Download(id string) (*os.File, error) {
	file, err := s.backupPath(id)
	if err != nil {
		return nil, err
	}
	var phase string
	if err = s.DB.QueryRow(`SELECT phase FROM recovery_backups WHERE id=?`, id).Scan(&phase); err != nil || (phase != "complete" && phase != "imported") {
		return nil, fmt.Errorf("archive is not complete")
	}
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("archive unavailable")
	}
	return os.Open(file)
}
func (s *RecoveryService) Import(input io.Reader) (RecoveryBackup, error) {
	if err := os.MkdirAll(filepath.Join(s.Directory, "archives"), 0700); err != nil {
		return RecoveryBackup{}, err
	}
	item := RecoveryBackup{ID: uuid.NewString(), Phase: "imported", CreatedAt: time.Now().Unix()}
	file, _ := s.backupPath(item.ID)
	out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return item, err
	}
	keep := false
	defer func() {
		out.Close()
		if !keep {
			os.Remove(file)
		}
	}()
	hash := sha256.New()
	item.Size, err = io.Copy(io.MultiWriter(out, hash), io.LimitReader(input, recoveryMaximum+(32<<20)))
	if err != nil || item.Size >= recoveryMaximum+(32<<20) {
		return item, fmt.Errorf("archive upload failed or exceeded size limit")
	}
	if err = out.Sync(); err != nil {
		return item, err
	}
	out.Close()
	header, e := os.Open(file)
	if e != nil {
		return item, e
	}
	magic := make([]byte, 8)
	_, e = io.ReadFull(header, magic)
	header.Close()
	if e != nil || string(magic) != recoveryMagic {
		return item, fmt.Errorf("not an encrypted DockTun recovery archive")
	}
	item.SHA256 = hex.EncodeToString(hash.Sum(nil))
	_, err = s.DB.Exec(`INSERT INTO recovery_backups(id,operation_id,phase,created_at,size,sha256) VALUES(?,'',?,?,?,?)`, item.ID, item.Phase, item.CreatedAt, item.Size, item.SHA256)
	keep = err == nil
	return item, err
}
func safeRemoteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("use an HTTPS collection URL without embedded credentials or query secrets")
	}
	return u, nil
}

func (s *RecoveryService) Inventory(ctx context.Context) (map[string]any, error) {
	var containerRows []struct {
		ID    string `json:"Id"`
		Names []string
	}
	var volumeRows struct{ Volumes []struct{ Name string } }
	if err := dockerJSON(ctx, "GET", "/containers/json?all=1", nil, &containerRows); err != nil {
		return nil, err
	}
	if err := dockerJSON(ctx, "GET", "/volumes", nil, &volumeRows); err != nil {
		return nil, err
	}
	containers := []map[string]string{}
	volumes := []map[string]string{}
	projects := []map[string]any{}
	for _, c := range containerRows {
		if len(c.Names) > 0 {
			containers = append(containers, map[string]string{"id": c.ID, "name": strings.TrimPrefix(c.Names[0], "/")})
		}
	}
	for _, v := range volumeRows.Volumes {
		volumes = append(volumes, map[string]string{"name": v.Name})
	}
	rows, err := s.DB.Query(`SELECT id,name,path FROM projects ORDER BY name LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var name, path string
		if err = rows.Scan(&id, &name, &path); err != nil {
			return nil, err
		}
		projects = append(projects, map[string]any{"id": id, "name": name, "path": path})
	}
	return map[string]any{"containers": containers, "volumes": volumes, "projects": projects}, rows.Err()
}

// Runs at core startup after operation reconciliation. Never replays a restore
// or restarts containers automatically after an unclean shutdown.
func (s *RecoveryService) ReconcileStartup() error {
	if _, err := s.DB.Exec(`UPDATE recovery_backups SET phase='failed',last_error='Backup interrupted by core restart; inspect operation recovery resources' WHERE phase='creating' AND operation_id IN (SELECT id FROM operations WHERE phase='interrupted')`); err != nil {
		return err
	}
	directory := filepath.Join(s.Directory, "tmp")
	root, err := os.OpenRoot(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), "backup-") || strings.HasPrefix(entry.Name(), "restore-")) {
			if err = root.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}
