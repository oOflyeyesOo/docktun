package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RecoverySettings struct {
	ActorID       int             `json:"actor_id,omitempty"`
	RemoteURL     string          `json:"remote_url"`
	RetainCount   int             `json:"retain_count"`
	RetainDays    int             `json:"retain_days"`
	ScheduleHours int             `json:"schedule_hours"`
	Selection     RecoveryRequest `json:"selection"`
}

func (s *RecoveryService) Settings() (RecoverySettings, error) {
	settings := RecoverySettings{RetainCount: 14, RetainDays: 30}
	var raw string
	err := s.DB.QueryRow(`SELECT value FROM recovery_settings WHERE id=1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err = json.Unmarshal([]byte(raw), &settings); err != nil {
		return settings, err
	}
	settings.Selection.Passphrase = ""
	return settings, nil
}
func (s *RecoveryService) SaveSettings(settings RecoverySettings) error {
	settings.Selection.Passphrase = ""
	if settings.ScheduleHours > 0 {
		if settings.ActorID < 1 {
			return fmt.Errorf("save the schedule from an authenticated administrator account")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for i, selection := range settings.Selection.Databases {
			c, e := inspectUpdateContainer(ctx, selection.Container)
			if e != nil {
				return fmt.Errorf("scheduled database target unavailable")
			}
			settings.Selection.Databases[i].Container = strings.TrimPrefix(c.Name, "/")
		}
		for i, id := range settings.Selection.StopContainers {
			c, e := inspectUpdateContainer(ctx, id)
			if e != nil {
				return fmt.Errorf("scheduled stop target unavailable")
			}
			settings.Selection.StopContainers[i] = strings.TrimPrefix(c.Name, "/")
		}
	}
	if settings.RetainCount < 1 || settings.RetainCount > 1000 || settings.RetainDays < 1 || settings.RetainDays > 3650 || settings.ScheduleHours < 0 || settings.ScheduleHours > 168 {
		return fmt.Errorf("retention and schedule are outside allowed bounds")
	}
	if settings.RemoteURL != "" {
		if _, err := safeRemoteURL(settings.RemoteURL); err != nil {
			return err
		}
	}
	if settings.ScheduleHours > 0 {
		if _, err := s.scheduleKey(); err != nil {
			return err
		}
		if !settings.Selection.Instance && len(settings.Selection.Projects)+len(settings.Selection.Volumes)+len(settings.Selection.Databases) == 0 {
			return fmt.Errorf("scheduled backups need explicit resource selections")
		}
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO recovery_settings(id,value) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value`, string(raw))
	return err
}
func (s *RecoveryService) scheduleKey() (string, error) {
	file := strings.TrimSpace(os.Getenv("DOCKTUN_BACKUP_KEY_FILE"))
	if file == "" {
		return "", fmt.Errorf("scheduled backups require a separately mounted DOCKTUN_BACKUP_KEY_FILE")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("backup key file is unavailable")
	}
	key := strings.TrimSpace(string(raw))
	if len(key) < 12 || len(key) > 1024 {
		return "", fmt.Errorf("backup key file must contain a 12–1024 byte passphrase")
	}
	return key, nil
}
func (s *RecoveryService) remoteObjectURL(id string) (string, error) {
	settings, err := s.Settings()
	if err != nil {
		return "", err
	}
	base, err := safeRemoteURL(settings.RemoteURL)
	if err != nil {
		return "", err
	}
	var namespace string
	if s.DB.QueryRow(`SELECT value FROM system_settings WHERE key='recovery_namespace'`).Scan(&namespace) != nil {
		namespace = uuid.NewString()
		if _, err = s.DB.Exec(`INSERT OR IGNORE INTO system_settings(key,value) VALUES('recovery_namespace',?)`, namespace); err != nil {
			return "", err
		}
		if err = s.DB.QueryRow(`SELECT value FROM system_settings WHERE key='recovery_namespace'`).Scan(&namespace); err != nil {
			return "", err
		}
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/docktun-" + namespace + "-" + id + ".dtrec"
	base.RawPath = ""
	return base.String(), nil
}
func recoveryRemoteClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func recoveryRemoteRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("invalid remote backup destination")
	}
	if file := os.Getenv("DOCKTUN_BACKUP_REMOTE_AUTH_FILE"); file != "" {
		raw, e := os.ReadFile(file)
		if e != nil {
			return nil, fmt.Errorf("remote authorization file unavailable")
		}
		authorization := strings.TrimSpace(string(raw))
		if strings.ContainsAny(authorization, "\r\n") {
			return nil, fmt.Errorf("invalid remote authorization file")
		}
		req.Header.Set("Authorization", authorization)
	}
	return req, nil
}
func (s *RecoveryService) UploadRemote(ctx context.Context, id string) error {
	endpoint, err := s.remoteObjectURL(id)
	if err != nil {
		return err
	}
	file, err := s.Download(id)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	var expected string
	if err = s.DB.QueryRow(`SELECT sha256 FROM recovery_backups WHERE id=?`, id).Scan(&expected); err != nil {
		return err
	}
	req, err := recoveryRemoteRequest(ctx, "PUT", endpoint, file)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-DockTun-SHA256", expected)
	response, err := s.remoteHTTP().Do(req)
	if err != nil {
		return fmt.Errorf("remote upload failed; check destination and authorization")
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("remote upload returned HTTP %d", response.StatusCode)
	}
	// Verify the object by reading it back. A successful PUT alone is not proof
	// that an operator can retrieve a complete encrypted archive.
	verify, err := recoveryRemoteRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}
	response, err = s.remoteHTTP().Do(verify)
	if err != nil {
		return fmt.Errorf("remote archive read-back failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("remote archive read-back returned HTTP %d", response.StatusCode)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(response.Body, info.Size()+1))
	if err != nil || size != info.Size() || hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("remote archive checksum did not match")
	}
	_, err = s.DB.Exec(`UPDATE recovery_backups SET remote_uploaded_at=? WHERE id=?`, time.Now().Unix(), id)
	return err
}
func (s *RecoveryService) FetchRemote(ctx context.Context, id string) (RecoveryBackup, error) {
	if _, err := s.backupPath(id); err != nil {
		return RecoveryBackup{}, err
	}
	endpoint, err := s.remoteObjectURL(id)
	if err != nil {
		return RecoveryBackup{}, err
	}
	req, err := recoveryRemoteRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return RecoveryBackup{}, err
	}
	response, err := s.remoteHTTP().Do(req)
	if err != nil {
		return RecoveryBackup{}, fmt.Errorf("remote archive fetch failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return RecoveryBackup{}, fmt.Errorf("remote archive unavailable")
	}
	return s.Import(response.Body)
}
func (s *RecoveryService) ApplyRetention(now time.Time, actor ...int) error {
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	rows, err := s.DB.Query(`SELECT id,created_at,remote_uploaded_at,restored_at FROM recovery_backups WHERE phase IN ('complete','imported') ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return err
	}
	type retained struct {
		id               string
		created          int64
		remote, restored bool
	}
	items := []retained{}
	lastVerified := ""
	for rows.Next() {
		var item retained
		var remote, restored interface{}
		if err = rows.Scan(&item.id, &item.created, &remote, &restored); err != nil {
			rows.Close()
			return err
		}
		item.remote = remote != nil
		item.restored = restored != nil
		if item.restored && lastVerified == "" {
			lastVerified = item.id
		}
		items = append(items, item)
	}
	rows.Close()
	for index, item := range items {
		if index < settings.RetainCount || now.Sub(time.Unix(item.created, 0)) < time.Duration(settings.RetainDays)*24*time.Hour || item.id == lastVerified {
			continue
		}
		if !item.remote {
			continue
		}
		file, e := s.backupPath(item.id)
		if e != nil {
			return e
		}
		info, e := os.Lstat(file)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("retention refused an unexpected archive path")
		}
		if e = os.Remove(file); e != nil {
			return e
		}
		if _, e = s.DB.Exec(`UPDATE recovery_backups SET phase='not_local',last_error='Local copy retired by retention; recover from verified external storage' WHERE id=?`, item.id); e != nil {
			return e
		}
		if len(actor) > 0 && actor[0] > 0 {
			NewAuditLogger(s.DB).Log(actor[0], "recovery_retention", item.id, "system")
		}
	}
	return nil
}
func (s *RecoveryService) RunSchedules(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			settings, err := s.Settings()
			if err != nil || settings.ScheduleHours == 0 || settings.ActorID < 1 || s.Config.RecoveryMode {
				continue
			}
			var latest int64
			_ = s.DB.QueryRow(`SELECT COALESCE(max(created_at),0) FROM recovery_backups WHERE phase<>'imported'`).Scan(&latest)
			if now.Unix()-latest < int64(settings.ScheduleHours)*3600 {
				continue
			}
			key, err := s.scheduleKey()
			if err != nil {
				continue
			}
			request := settings.Selection
			request.Passphrase = key
			request.ActorID = settings.ActorID
			if item, e := s.StartBackup(request); e == nil {
				NewAuditLogger(s.DB).Log(settings.ActorID, "recovery_scheduled", item.ID, "scheduler")
			}
			request.Passphrase = ""
		}
	}
}
func (s *RecoveryService) afterBackup(id string, actor int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	settings, err := s.Settings()
	if err == nil && settings.RemoteURL != "" {
		if err = s.UploadRemote(ctx, id); err != nil {
			_, _ = s.DB.Exec(`UPDATE recovery_backups SET last_error='Encrypted local backup completed, but remote upload/read-back verification failed' WHERE id=?`, id)
		}
		if actor > 0 {
			action := "recovery_auto_remote_verified"
			if err != nil {
				action = "recovery_auto_remote_failed"
			}
			NewAuditLogger(s.DB).Log(actor, action, id, "system")
		}
	}
	_ = s.ApplyRetention(time.Now(), actor)
}
func (s *RecoveryService) DeleteLocal(id string) error {
	file, err := s.backupPath(id)
	if err != nil {
		return err
	}
	var phase string
	if err = s.DB.QueryRow(`SELECT phase FROM recovery_backups WHERE id=?`, id).Scan(&phase); err != nil {
		return err
	}
	if phase == "creating" {
		return fmt.Errorf("backup is still running")
	}
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || filepath.Dir(file) != filepath.Join(s.Directory, "archives") {
		return fmt.Errorf("unexpected backup path")
	}
	if err = os.Remove(file); err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE recovery_backups SET phase='not_local',last_error='Local archive removed by administrator' WHERE id=?`, id)
	return err
}

func (s *RecoveryService) DownloadTicket(id string, user int) (string, error) {
	file, err := s.Download(id)
	if err != nil {
		return "", err
	}
	file.Close()
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(ticket))
	_, err = s.DB.Exec(`INSERT INTO recovery_download_tickets(hash,archive_id,user_id,expires_at) VALUES(?,?,?,?)`, hex.EncodeToString(sum[:]), id, user, time.Now().Add(time.Minute).Unix())
	if err != nil {
		return "", err
	}
	_, _ = s.DB.Exec(`DELETE FROM recovery_download_tickets WHERE expires_at<=?`, time.Now().Unix())
	return ticket, nil
}
func (s *RecoveryService) ConsumeDownload(id, ticket string) error {
	sum := sha256.Sum256([]byte(ticket))
	var archive string
	err := s.DB.QueryRow(`DELETE FROM recovery_download_tickets WHERE hash=? AND archive_id=? AND expires_at>? RETURNING archive_id`, hex.EncodeToString(sum[:]), id, time.Now().Unix()).Scan(&archive)
	if err != nil {
		return fmt.Errorf("download authorization expired or was used")
	}
	return nil
}

func (s *RecoveryService) remoteHTTP() *http.Client {
	if s.remoteClient != nil {
		return s.remoteClient
	}
	return recoveryRemoteClient()
}
