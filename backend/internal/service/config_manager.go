package service

import (
	"fmt"
	"os"
	"strings"

	"docktun/internal/config"
	"docktun/internal/db"
	"gopkg.in/yaml.v3"
)

type ConfigBackup struct {
	ID        int    `json:"id"`
	Content   string `json:"content"`
	UserID    int    `json:"user_id"`
	CreatedAt string `json:"created_at"`
}

type ConfigManager struct {
	cfg *config.Config
	db  *db.DB
}

func NewConfigManager(cfg *config.Config, db *db.DB) *ConfigManager {
	return &ConfigManager{cfg: cfg, db: db}
}

func (m *ConfigManager) Read() (string, error) {
	data, err := os.ReadFile(m.cfg.CloudflaredConfig)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *ConfigManager) EnsureInitialBackup() {
	var count int
	_ = m.db.QueryRow(`SELECT COUNT(*) FROM config_backups`).Scan(&count)
	if count == 0 {
		current, err := m.Read()
		if err == nil && len(current) > 0 {
			var adminID int
			_ = m.db.QueryRow(`SELECT id FROM users ORDER BY id ASC LIMIT 1`).Scan(&adminID)
			_ = m.CreateBackup(current, adminID)
		}
	}
}

func (m *ConfigManager) CreateBackup(content string, userID int) error {
	if content == "" {
		var err error
		content, err = m.Read()
		if err != nil {
			return err
		}
	}
	if userID <= 0 {
		var adminID int
		if err := m.db.QueryRow(`SELECT id FROM users ORDER BY id ASC LIMIT 1`).Scan(&adminID); err == nil && adminID > 0 {
			userID = adminID
		}
	}

	var err error
	if userID > 0 {
		_, err = m.db.Exec(`INSERT INTO config_backups (content, user_id) VALUES (?, ?)`, content, userID)
	} else {
		_, err = m.db.Exec(`INSERT INTO config_backups (content, user_id) VALUES (?, NULL)`, content)
	}
	if err != nil {
		return err
	}
	// Retain the 50 most recent backups
	m.db.Exec(`DELETE FROM config_backups WHERE id NOT IN (SELECT id FROM config_backups ORDER BY created_at DESC LIMIT 50)`)
	return nil
}

func (m *ConfigManager) DeleteBackup(id int) error {
	_, err := m.db.Exec(`DELETE FROM config_backups WHERE id = ?`, id)
	return err
}

func (m *ConfigManager) Write(content string, userID int) error {
	// Normalize CRLF / CR to pure UNIX LF
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	if err := m.Validate(content); err != nil {
		return fmt.Errorf("refusing to write invalid yaml: %w", err)
	}

	current, err := m.Read()
	if err == nil && current != "" {
		_ = m.CreateBackup(current, userID)
	}

	return os.WriteFile(m.cfg.CloudflaredConfig, []byte(content), 0644)
}

func (m *ConfigManager) Validate(content string) error {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	var out map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &out); err != nil {
		return fmt.Errorf("invalid yaml syntax: %w", err)
	}

	// Validate ingress structure if present
	if rawIngress, ok := out["ingress"]; ok {
		ingressList, ok := rawIngress.([]interface{})
		if !ok || len(ingressList) == 0 {
			return fmt.Errorf("'ingress' must be a non-empty list of rules")
		}

		// The final rule must be a catch-all service rule
		lastRule, ok := ingressList[len(ingressList)-1].(map[string]interface{})
		if !ok {
			return fmt.Errorf("the final ingress rule must be a mapping with 'service' (e.g. service: http_status:404)")
		}
		if _, hasSvc := lastRule["service"]; !hasSvc {
			return fmt.Errorf("the final ingress rule must specify a 'service'")
		}
		if host, hasHost := lastRule["hostname"]; hasHost && host != "" && host != nil {
			return fmt.Errorf("the final ingress rule must be a catch-all rule without a hostname (e.g. - service: http_status:404)")
		}
	}

	return nil
}

func (m *ConfigManager) ListBackups(limit, offset int) ([]ConfigBackup, error) {
	rows, err := m.db.Query(`SELECT id, content, COALESCE(user_id, 0), created_at FROM config_backups ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backups := []ConfigBackup{}
	for rows.Next() {
		var b ConfigBackup
		if err := rows.Scan(&b.ID, &b.Content, &b.UserID, &b.CreatedAt); err == nil {
			backups = append(backups, b)
		}
	}
	return backups, nil
}

func (m *ConfigManager) GetBackup(id int) (*ConfigBackup, error) {
	var b ConfigBackup
	err := m.db.QueryRow(`SELECT id, content, COALESCE(user_id, 0), created_at FROM config_backups WHERE id = ?`, id).
		Scan(&b.ID, &b.Content, &b.UserID, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}
