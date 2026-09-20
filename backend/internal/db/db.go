package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		// The database holds password hashes, session tokens and the alert
		// channel secrets, so nobody but the panel's own user may read it.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		f.Close()
	}

	// Pragmas are set in the DSN so they apply to every pooled connection:
	// SQLite leaves foreign keys OFF by default, which would silently disable
	// the ON DELETE SET NULL on time_restrictions.exempt_group_id.
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := conn.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	// ALTER TABLE ADD COLUMN has no "IF NOT EXISTS" in SQLite, so a DB created
	// before a column existed is upgraded here. "duplicate column" just means
	// it is already up to date; anything else is a real failure.
	for _, ddl := range columnMigrations {
		if _, err := conn.Exec(ddl); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, fmt.Errorf("migrate (%s): %w", ddl, err)
		}
	}

	// Databases made by older versions, and their WAL files, may be readable
	// by everyone; tighten them.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600)
	}

	return conn, nil
}

// columnMigrations add columns to tables that older versions already created.
// Existing rows get the DEFAULT, so the pre-existing single admin account
// keeps working as an admin.
var columnMigrations = []string{
	`ALTER TABLE time_restrictions ADD COLUMN exempt_group_id INTEGER REFERENCES ip_groups(id) ON DELETE SET NULL`,
	`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin'`,
	`ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE users ADD COLUMN failed_logins INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE users ADD COLUMN locked_until INTEGER NOT NULL DEFAULT 0`,
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ip_groups (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	members TEXT NOT NULL,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS time_restrictions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	domains TEXT NOT NULL,
	days TEXT NOT NULL,
	start_time TEXT NOT NULL,
	end_time TEXT NOT NULL,
	exempt_cidrs TEXT NOT NULL DEFAULT '[]',
	exempt_group_id INTEGER REFERENCES ip_groups(id) ON DELETE SET NULL,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS config_versions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	comment TEXT NOT NULL,
	content TEXT NOT NULL
);

-- Server-side login sessions. Only a SHA-256 of the cookie token is stored, so
-- a leaked database can't be replayed as live sessions.
CREATE TABLE IF NOT EXISTS sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	token_hash TEXT NOT NULL UNIQUE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	last_seen INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT ''
);

-- Who did what. Written by the API for every state-changing request and for
-- login attempts; request bodies are never stored (they can hold passwords).
CREATE TABLE IF NOT EXISTS audit_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	username TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	status INTEGER NOT NULL DEFAULT 0,
	detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_log_ts ON audit_log(ts);

-- Named, typed ACL objects (a set of domains, IPs, ports, ...) that access
-- rules refer to. Values are a JSON array of strings.
CREATE TABLE IF NOT EXISTS acl_objects (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	type TEXT NOT NULL,
	acl_values TEXT NOT NULL,
	case_insensitive INTEGER NOT NULL DEFAULT 1,
	description TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

-- User-defined http_access rules, evaluated in position order. terms is a
-- JSON array of {"acl_id": n, "negate": bool}; all terms must match.
CREATE TABLE IF NOT EXISTS access_rules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	position INTEGER NOT NULL,
	action TEXT NOT NULL,
	terms TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	comment TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

-- External domain block lists that are downloaded and refreshed on a schedule.
CREATE TABLE IF NOT EXISTS blocklist_sources (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	url TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	interval_hours INTEGER NOT NULL DEFAULT 24,
	last_fetched INTEGER NOT NULL DEFAULT 0,
	last_status TEXT NOT NULL DEFAULT '',
	entry_count INTEGER NOT NULL DEFAULT 0,
	content_hash TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

-- Traffic statistics, aggregated from squid's access.log as it is read. One row
-- per hour, client, user and domain: enough for top lists, per-user traffic,
-- hit ratio and timelines, without storing every request.
CREATE TABLE IF NOT EXISTS stats_hourly (
	hour INTEGER NOT NULL,
	client TEXT NOT NULL,
	user TEXT NOT NULL,
	domain TEXT NOT NULL,
	requests INTEGER NOT NULL,
	bytes INTEGER NOT NULL,
	hits INTEGER NOT NULL,
	hit_bytes INTEGER NOT NULL,
	denied INTEGER NOT NULL,
	errors INTEGER NOT NULL,
	PRIMARY KEY (hour, client, user, domain)
);
CREATE INDEX IF NOT EXISTS idx_stats_hourly_hour ON stats_hourly(hour);

-- The most recent requests squid refused (403): who tried to reach what.
CREATE TABLE IF NOT EXISTS denied_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts INTEGER NOT NULL,
	client TEXT NOT NULL,
	user TEXT NOT NULL,
	method TEXT NOT NULL,
	url TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_denied_log_ts ON denied_log(ts);

-- Where the log reader stopped, so a restart neither re-counts nor skips lines.
CREATE TABLE IF NOT EXISTS ingest_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	path TEXT NOT NULL,
	head TEXT NOT NULL,
	offset INTEGER NOT NULL
);

-- Alert channels and thresholds (one JSON document), per-condition state and
-- the history of what was sent.
CREATE TABLE IF NOT EXISTS alert_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	config TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS alert_state (
	key TEXT PRIMARY KEY,
	firing INTEGER NOT NULL DEFAULT 0,
	since INTEGER NOT NULL DEFAULT 0,
	last_notified INTEGER NOT NULL DEFAULT 0,
	detail TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS alert_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts INTEGER NOT NULL,
	key TEXT NOT NULL,
	kind TEXT NOT NULL,
	message TEXT NOT NULL,
	delivery TEXT NOT NULL DEFAULT ''
);

-- Proxy users: the passwd file holds the credentials, this holds everything
-- else about the account. A user without a row has the defaults.
CREATE TABLE IF NOT EXISTS proxy_users (
	username TEXT PRIMARY KEY,
	disabled INTEGER NOT NULL DEFAULT 0,
	expires_at INTEGER NOT NULL DEFAULT 0,       -- unix seconds, 0 = never
	daily_quota_mb INTEGER NOT NULL DEFAULT 0,   -- 0 = unlimited
	note TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS user_groups (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS user_group_members (
	group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
	username TEXT NOT NULL,
	PRIMARY KEY (group_id, username)
);

-- Speed and download-size limits. spec is a JSON document (who it applies to
-- and the numbers), see squid/limits.go.
CREATE TABLE IF NOT EXISTS limit_rules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	kind TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	spec TEXT NOT NULL
);
`
