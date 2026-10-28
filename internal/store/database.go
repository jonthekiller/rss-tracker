// Package store provides the SQLite persistence layer. The schema and the
// migration strategy are copied verbatim from the original TypeScript
// implementation (src/db/database.ts) so both binaries can open the same
// database file interchangeably.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// migration is one schema version step.
type migration struct {
	version int
	name    string
	up      string
}

// MIGRATIONS must stay byte-identical to the TypeScript MIGRATIONS array.
var MIGRATIONS = []migration{
	{
		version: 1,
		name:    "initial-schema",
		up: `
      CREATE TABLE feeds (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL DEFAULT 'Unknown',
        url TEXT NOT NULL,
        guild_id TEXT NOT NULL,
        channel_id TEXT NOT NULL,
        enabled INTEGER NOT NULL DEFAULT 1,
        interval_minutes INTEGER NOT NULL DEFAULT 10,
        next_check_at INTEGER NOT NULL DEFAULT 0,
        template_text TEXT NOT NULL DEFAULT '**{title}**\n{link}',
        embed_json TEXT,
        batch_mode TEXT NOT NULL DEFAULT 'auto' CHECK (batch_mode IN ('auto','individual','digest')),
        batch_threshold INTEGER NOT NULL DEFAULT 5,
        batch_max_items INTEGER NOT NULL DEFAULT 15,
        max_age_days INTEGER NOT NULL DEFAULT 30,
        etag TEXT,
        last_modified TEXT,
        last_checked_at INTEGER,
        last_error TEXT,
        consecutive_failures INTEGER NOT NULL DEFAULT 0,
        failed_notified INTEGER NOT NULL DEFAULT 0,
        pause_reason TEXT,
        created_at INTEGER NOT NULL DEFAULT (unixepoch())
      );
      CREATE UNIQUE INDEX idx_feeds_url_channel ON feeds (url, channel_id);
      CREATE INDEX idx_feeds_next_check ON feeds (next_check_at) WHERE enabled = 1;

      CREATE TABLE articles_seen (
        feed_id TEXT NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
        article_key TEXT NOT NULL,
        published_at INTEGER,
        first_seen_at INTEGER NOT NULL DEFAULT (unixepoch()),
        PRIMARY KEY (feed_id, article_key)
      );

      CREATE TABLE filters (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        feed_id TEXT NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
        kind TEXT NOT NULL CHECK (kind IN ('include','exclude')),
        field TEXT NOT NULL DEFAULT 'any' CHECK (field IN ('title','description','any')),
        pattern TEXT NOT NULL,
        is_regex INTEGER NOT NULL DEFAULT 0
      );
      CREATE INDEX idx_filters_feed ON filters (feed_id);

      CREATE TABLE settings (
        key TEXT PRIMARY KEY,
        value TEXT
      );

      CREATE TABLE kv (
        key TEXT PRIMARY KEY,
        value TEXT
      );
    `,
	},
	{
		version: 2,
		name:    "per-channel-settings",
		up: `
      CREATE TABLE channel_settings (
        channel_id TEXT NOT NULL,
        key TEXT NOT NULL,
        value TEXT,
        PRIMARY KEY (channel_id, key)
      );
    `,
	},
	{
		// Non-RSS sources: JSON APIs and HTML pages. rss keeps the legacy
		// behavior; the TS binary ignores these columns (explicit SELECTs).
		version: 3,
		name:    "non-rss-sources",
		up: `
      ALTER TABLE feeds ADD COLUMN source_type TEXT NOT NULL DEFAULT 'rss'
        CHECK (source_type IN ('rss','json','html','auto'));
      ALTER TABLE feeds ADD COLUMN extract_config TEXT;
    `,
	},
}

// OpenOptions configures Open.
type OpenOptions struct {
	DBPath string
	// EnsureDir creates the parent directory when true (tests pass false).
	EnsureDir bool
}

// Open opens (and migrates) the SQLite database with the pragmas used by the
// TS version. A single connection serializes writers and avoids SQLITE_BUSY.
func Open(options OpenOptions) (*sql.DB, error) {
	if options.EnsureDir {
		if err := os.MkdirAll(filepath.Dir(options.DBPath), 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", options.DBPath)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite handles concurrent reads, but a single connection keeps
	// writes serialized (the bot is single-process) and mirrors better-sqlite3
	// synchronous behavior.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", pragma, err)
		}
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate applies pending migrations inside transactions.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`
    CREATE TABLE IF NOT EXISTS schema_migrations (
      version INTEGER PRIMARY KEY,
      name TEXT NOT NULL,
      applied_at INTEGER NOT NULL DEFAULT (unixepoch())
    );
  `); err != nil {
		return err
	}

	applied := map[int]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return err
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, m := range MIGRATIONS {
		if applied[m.version] {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.up); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name,
		); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// KVSet stores a key/value heartbeat entry.
func KVSet(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// KVGet reads a key from the kv table (nil when absent).
func KVGet(db *sql.DB, key string) (*string, error) {
	var value sql.NullString
	err := db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !value.Valid {
		return nil, nil
	}
	v := value.String
	return &v, nil
}
