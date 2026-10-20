package service

import (
	"os"

	"github.com/luzyver/tunlify/internal/config"
	"github.com/luzyver/tunlify/internal/db"
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
	m.db.QueryRow(`SELECT COUNT(*) FROM config_backups`).Scan(&count)
	if count == 0 {
		current, err := m.Read()
		if err == nil && len(current) > 0 {
			var adminID int
			m.db.QueryRow(`SELECT id FROM users ORDER BY id ASC LIMIT 1`).Scan(&adminID)
			if adminID == 0 {
				adminID = 1
			}
			m.CreateBackup(current, adminID)
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
	_, err := m.db.Exec(`INSERT INTO config_backups (content, user_id) VALUES (?, ?)`, content, userID)
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
	current, err := m.Read()
	if err == nil && current != "" {
		m.CreateBackup(current, userID)
	}

	return os.WriteFile(m.cfg.CloudflaredConfig, []byte(content), 0644)
}

func (m *ConfigManager) Validate(content string) error {
	var out interface{}
	if err := yaml.Unmarshal([]byte(content), &out); err != nil {
		return err
	}
	return nil
}

func (m *ConfigManager) ListBackups(limit, offset int) ([]ConfigBackup, error) {
	rows, err := m.db.Query(`SELECT id, content, user_id, created_at FROM config_backups ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
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
	err := m.db.QueryRow(`SELECT id, content, user_id, created_at FROM config_backups WHERE id = ?`, id).
		Scan(&b.ID, &b.Content, &b.UserID, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}
