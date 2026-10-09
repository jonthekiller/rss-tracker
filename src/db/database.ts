import fs from 'node:fs'
import path from 'node:path'
import DatabaseConstructor from 'better-sqlite3'

export type Database = DatabaseConstructor.Database

interface Migration {
  version: number
  name: string
  up: string
}

const MIGRATIONS: Migration[] = [
  {
    version: 1,
    name: 'initial-schema',
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
        template_text TEXT NOT NULL DEFAULT '**{title}**\\n{link}',
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
    name: 'per-channel-settings',
    up: `
      CREATE TABLE channel_settings (
        channel_id TEXT NOT NULL,
        key TEXT NOT NULL,
        value TEXT,
        PRIMARY KEY (channel_id, key)
      );
    `,
  },
]

export interface OpenDatabaseOptions {
  dbPath: string
  /** When false (tests), fs.mkdir is skipped. */
  ensureDir?: boolean
}

export function openDatabase(options: OpenDatabaseOptions): Database {
  if (options.ensureDir !== false) {
    fs.mkdirSync(path.dirname(options.dbPath), { recursive: true })
  }
  const db = new DatabaseConstructor(options.dbPath)
  db.pragma('journal_mode = WAL')
  db.pragma('foreign_keys = ON')
  db.pragma('busy_timeout = 5000')
  migrate(db)
  return db
}

export function migrate(db: Database): void {
  db.exec(`
    CREATE TABLE IF NOT EXISTS schema_migrations (
      version INTEGER PRIMARY KEY,
      name TEXT NOT NULL,
      applied_at INTEGER NOT NULL DEFAULT (unixepoch())
    );
  `)
  const applied = new Set(
    db
      .prepare('SELECT version FROM schema_migrations')
      .all()
      .map((r) => (r as { version: number }).version),
  )

  for (const migration of MIGRATIONS) {
    if (applied.has(migration.version)) continue
    const apply = db.transaction(() => {
      db.exec(migration.up)
      db.prepare('INSERT INTO schema_migrations (version, name) VALUES (?, ?)').run(
        migration.version,
        migration.name,
      )
    })
    apply()
  }
}

export function closeDatabase(db: Database): void {
  db.close()
}

export function kvSet(db: Database, key: string, value: string): void {
  db.prepare(
    'INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value',
  ).run(key, value)
}

export function kvGet(db: Database, key: string): string | null {
  const row = db.prepare('SELECT value FROM kv WHERE key = ?').get(key) as
    | { value: string }
    | undefined
  return row?.value ?? null
}
