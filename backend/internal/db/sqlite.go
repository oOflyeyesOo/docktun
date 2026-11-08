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

		CREATE TABLE IF NOT EXISTS operations (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, target TEXT NOT NULL, phase TEXT NOT NULL,
 message TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 owner TEXT NOT NULL, snapshot TEXT NOT NULL DEFAULT '{}'
 );
 CREATE TABLE IF NOT EXISTS operation_locks (target TEXT PRIMARY KEY, operation_id TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS recovery_backups (
 id TEXT PRIMARY KEY, operation_id TEXT NOT NULL, phase TEXT NOT NULL, created_at INTEGER NOT NULL,
 size INTEGER NOT NULL DEFAULT 0, sha256 TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '{}',
 integrity_verified_at INTEGER, restored_at INTEGER, remote_uploaded_at INTEGER, last_error TEXT NOT NULL DEFAULT ''
 );
 CREATE TABLE IF NOT EXISTS recovery_download_tickets(hash TEXT PRIMARY KEY,archive_id TEXT NOT NULL,user_id INTEGER NOT NULL,expires_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS recovery_settings (id INTEGER PRIMARY KEY CHECK(id=1), value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS readiness_checks (container_name TEXT PRIMARY KEY, url TEXT NOT NULL, timeout_seconds INTEGER NOT NULL DEFAULT 5);
 CREATE TABLE IF NOT EXISTS alert_outbox (
 id TEXT PRIMARY KEY, event_key TEXT NOT NULL, kind TEXT NOT NULL, message TEXT NOT NULL,
 phase TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL, created_at INTEGER NOT NULL, delivered_at INTEGER,
 last_error TEXT NOT NULL DEFAULT ''
 );
 CREATE UNIQUE INDEX IF NOT EXISTS idx_pending_alert_key ON alert_outbox(event_key) WHERE phase IN ('pending','retry');
 CREATE TABLE IF NOT EXISTS monitor_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS auth_sessions (
            id TEXT PRIMARY KEY,
            user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            refresh_jti TEXT NOT NULL,
            expires_at INTEGER NOT NULL,
            revoked INTEGER NOT NULL DEFAULT 0
        );
        CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(user_id);

        CREATE TABLE IF NOT EXISTS token_revocations (
			jti TEXT PRIMARY KEY,
			expires_at DATETIME NOT NULL,
			revoked_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_token_revocations_expiry ON token_revocations(expires_at);

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

		CREATE TABLE IF NOT EXISTS project_compose_sources (
			project_id INTEGER PRIMARY KEY,
			files TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS project_storage_reviews (
			project_id INTEGER PRIMARY KEY,
			source TEXT NOT NULL DEFAULT ''
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

		CREATE TABLE IF NOT EXISTS tailscale_rule_cleanup (
			app TEXT NOT NULL,
			node TEXT NOT NULL,
			port INTEGER NOT NULL,
			target TEXT NOT NULL,
			PRIMARY KEY (app, node, port)
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
