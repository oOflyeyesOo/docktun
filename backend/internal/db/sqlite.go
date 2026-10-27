package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func New(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	parameters := url.Values{"_pragma": {
		"busy_timeout=5000", "foreign_keys=ON", "synchronous=NORMAL",
		"cache_size=-20000", "temp_store=MEMORY",
	}}
	uri.RawQuery = parameters.Encode()
	dsn := uri.String()
	if path == ":memory:" {
		dsn = "file::memory:?" + parameters.Encode()
	}
	// Driver parameters are applied when each pooled connection is opened.
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable SQLite WAL: %w", err)
	}

	d := &DB{conn}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			detail TEXT,
			ip_address TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		);

		CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at DESC);

		CREATE TABLE IF NOT EXISTS config_backups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			content TEXT NOT NULL,
			user_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		);

		CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			repo_url TEXT DEFAULT '',
			git_username TEXT DEFAULT '',
			git_token TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS container_exposures (
			container_name TEXT PRIMARY KEY,
			mode TEXT NOT NULL DEFAULT 'internal',
			hostname TEXT DEFAULT '',
			target TEXT DEFAULT '',
			private_type TEXT DEFAULT 'tailscale',
			private_host TEXT DEFAULT '',
			private_port INTEGER DEFAULT 80,
			notes TEXT DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS system_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS container_updates (
			container_id TEXT PRIMARY KEY,
			container_name TEXT NOT NULL,
			image TEXT NOT NULL,
			current_digest TEXT DEFAULT '',
			latest_digest TEXT DEFAULT '',
			update_available INTEGER DEFAULT 0,
			checked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			error TEXT DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_container_updates_name ON container_updates(container_name);
		CREATE INDEX IF NOT EXISTS idx_projects_name ON projects(name);
		CREATE INDEX IF NOT EXISTS idx_projects_path ON projects(path);
	`)
	if err != nil {
		return err
	}

	_, _ = d.Exec(`ALTER TABLE container_exposures ADD COLUMN tailscale_node TEXT DEFAULT ''`)
	// Safe column additions for Zero Trust Access support on container_exposures
	_, _ = d.Exec(`ALTER TABLE container_exposures ADD COLUMN zero_trust_enabled INTEGER DEFAULT 0`)
	_, _ = d.Exec(`ALTER TABLE container_exposures ADD COLUMN zero_trust_emails TEXT DEFAULT ''`)
	_, _ = d.Exec(`ALTER TABLE container_exposures ADD COLUMN zero_trust_app_id TEXT DEFAULT ''`)
	_, _ = d.Exec(`ALTER TABLE container_exposures ADD COLUMN zero_trust_auth_type TEXT DEFAULT 'email'`)
	_, _ = d.Exec(`ALTER TABLE container_exposures ADD COLUMN zero_trust_session TEXT DEFAULT '24h'`)

	return nil
}
