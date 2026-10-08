import type { Database } from './database.js'

export type BatchMode = 'auto' | 'individual' | 'digest'
export type FilterKind = 'include' | 'exclude'
export type FilterField = 'title' | 'description' | 'any'

export interface Feed {
  id: string
  name: string
  url: string
  guildId: string
  channelId: string
  enabled: boolean
  intervalMinutes: number
  nextCheckAt: number
  templateText: string
  embedJson: string | null
  batchMode: BatchMode
  batchThreshold: number
  batchMaxItems: number
  maxAgeDays: number
  etag: string | null
  lastModified: string | null
  lastCheckedAt: number | null
  lastError: string | null
  consecutiveFailures: number
  failedNotified: boolean
  pauseReason: string | null
  createdAt: number
}

export interface FeedFilter {
  id: number
  feedId: string
  kind: FilterKind
  field: FilterField
  pattern: string
  isRegex: boolean
}

export interface NewFeed {
  id: string
  url: string
  guildId: string
  channelId: string
  name?: string
  templateText?: string
  embedJson?: string | null
  intervalMinutes?: number
  batchMode?: BatchMode
  batchThreshold?: number
  batchMaxItems?: number
  maxAgeDays?: number
  enabled?: boolean
  pauseReason?: string | null
}

function rowToFeed(row: Record<string, unknown>): Feed {
  return {
    id: row.id as string,
    name: row.name as string,
    url: row.url as string,
    guildId: row.guild_id as string,
    channelId: row.channel_id as string,
    enabled: row.enabled === 1,
    intervalMinutes: row.interval_minutes as number,
    nextCheckAt: row.next_check_at as number,
    templateText: row.template_text as string,
    embedJson: (row.embed_json as string | null) ?? null,
    batchMode: row.batch_mode as BatchMode,
    batchThreshold: row.batch_threshold as number,
    batchMaxItems: row.batch_max_items as number,
    maxAgeDays: row.max_age_days as number,
    etag: (row.etag as string | null) ?? null,
    lastModified: (row.last_modified as string | null) ?? null,
    lastCheckedAt: (row.last_checked_at as number | null) ?? null,
    lastError: (row.last_error as string | null) ?? null,
    consecutiveFailures: row.consecutive_failures as number,
    failedNotified: row.failed_notified === 1,
    pauseReason: (row.pause_reason as string | null) ?? null,
    createdAt: row.created_at as number,
  }
}

const FEED_COLUMNS = `id, name, url, guild_id, channel_id, enabled, interval_minutes, next_check_at,
  template_text, embed_json, batch_mode, batch_threshold, batch_max_items, max_age_days,
  etag, last_modified, last_checked_at, last_error, consecutive_failures, failed_notified,
  pause_reason, created_at`

export class FeedStore {
  constructor(private readonly db: Database) {}

  getFeed(id: string): Feed | null {
    const row = this.db.prepare(`SELECT ${FEED_COLUMNS} FROM feeds WHERE id = ?`).get(id) as
      | Record<string, unknown>
      | undefined
    return row ? rowToFeed(row) : null
  }

  getFeedsForGuild(guildId: string): Feed[] {
    const rows = this.db
      .prepare(`SELECT ${FEED_COLUMNS} FROM feeds WHERE guild_id = ? ORDER BY name, url`)
      .all(guildId) as Record<string, unknown>[]
    return rows.map(rowToFeed)
  }

  getFeedsForChannel(channelId: string): Feed[] {
    const rows = this.db
      .prepare(`SELECT ${FEED_COLUMNS} FROM feeds WHERE channel_id = ? ORDER BY name, url`)
      .all(channelId) as Record<string, unknown>[]
    return rows.map(rowToFeed)
  }

  getAllFeeds(): Feed[] {
    const rows = this.db
      .prepare(`SELECT ${FEED_COLUMNS} FROM feeds ORDER BY name, url`)
      .all() as Record<string, unknown>[]
    return rows.map(rowToFeed)
  }

  getFeedsDueNow(now: number, limit: number): Feed[] {
    const rows = this.db
      .prepare(
        `SELECT ${FEED_COLUMNS} FROM feeds WHERE enabled = 1 AND next_check_at <= ? ORDER BY next_check_at LIMIT ?`,
      )
      .all(now, limit) as Record<string, unknown>[]
    return rows.map(rowToFeed)
  }

  exists(url: string, channelId: string): boolean {
    return (
      this.db
        .prepare('SELECT 1 FROM feeds WHERE url = ? AND channel_id = ?')
        .get(url, channelId) !== undefined
    )
  }

  addFeed(feed: NewFeed): Feed {
    const interval = feed.intervalMinutes ?? 10
    this.db
      .prepare(
        `INSERT INTO feeds (id, name, url, guild_id, channel_id, enabled, interval_minutes, next_check_at,
          template_text, embed_json, batch_mode, batch_threshold, batch_max_items, max_age_days, pause_reason)
         VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .run(
        feed.id,
        feed.name ?? 'Unknown',
        feed.url,
        feed.guildId,
        feed.channelId,
        feed.enabled === false ? 0 : 1,
        interval,
        feed.templateText ?? '**{title}**\n{link}',
        feed.embedJson ?? null,
        feed.batchMode ?? 'auto',
        feed.batchThreshold ?? 5,
        feed.batchMaxItems ?? 15,
        feed.maxAgeDays ?? 30,
        feed.pauseReason ?? null,
      )
    return this.getFeed(feed.id) as Feed
  }

  updateFeed(id: string, patch: Partial<Feed>): void {
    const allowed: Partial<Record<keyof Feed, string>> = {
      name: 'name',
      channelId: 'channel_id',
      enabled: 'enabled',
      intervalMinutes: 'interval_minutes',
      nextCheckAt: 'next_check_at',
      templateText: 'template_text',
      embedJson: 'embed_json',
      batchMode: 'batch_mode',
      batchThreshold: 'batch_threshold',
      batchMaxItems: 'batch_max_items',
      maxAgeDays: 'max_age_days',
      etag: 'etag',
      lastModified: 'last_modified',
      lastCheckedAt: 'last_checked_at',
      lastError: 'last_error',
      consecutiveFailures: 'consecutive_failures',
      failedNotified: 'failed_notified',
      pauseReason: 'pause_reason',
    }
    const sets: string[] = []
    const values: unknown[] = []
    for (const [key, column] of Object.entries(allowed) as [keyof Feed, string][]) {
      if (patch[key] === undefined) continue
      let value = patch[key] as unknown
      if (typeof value === 'boolean') value = value ? 1 : 0
      sets.push(`${column} = ?`)
      values.push(value)
    }
    if (sets.length === 0) return
    values.push(id)
    this.db.prepare(`UPDATE feeds SET ${sets.join(', ')} WHERE id = ?`).run(...values)
  }

  deleteFeed(id: string): void {
    this.db.prepare('DELETE FROM feeds WHERE id = ?').run(id)
  }

  setNextCheck(id: string, nextCheckAt: number): void {
    this.db.prepare('UPDATE feeds SET next_check_at = ? WHERE id = ?').run(nextCheckAt, id)
  }

  markChecked(id: string, now: number, etag: string | null, lastModified: string | null): void {
    this.db
      .prepare(
        'UPDATE feeds SET last_checked_at = ?, etag = COALESCE(?, etag), last_modified = COALESCE(?, last_modified), last_error = NULL, consecutive_failures = 0, failed_notified = 0 WHERE id = ?',
      )
      .run(now, etag, lastModified, id)
  }

  markFailure(id: string, now: number, error: string): void {
    this.db
      .prepare(
        'UPDATE feeds SET last_checked_at = ?, last_error = ?, consecutive_failures = consecutive_failures + 1 WHERE id = ?',
      )
      .run(now, error.slice(0, 500), id)
  }

  // ---- filters ----

  getFilters(feedId: string): FeedFilter[] {
    return this.db
      .prepare('SELECT * FROM filters WHERE feed_id = ? ORDER BY id')
      .all(feedId)
      .map((row) => {
        const r = row as Record<string, unknown>
        return {
          id: r.id as number,
          feedId: r.feed_id as string,
          kind: r.kind as FilterKind,
          field: r.field as FilterField,
          pattern: r.pattern as string,
          isRegex: r.is_regex === 1,
        }
      })
  }

  addFilter(
    feedId: string,
    kind: FilterKind,
    field: FilterField,
    pattern: string,
    isRegex: boolean,
  ): void {
    this.db
      .prepare(
        'INSERT INTO filters (feed_id, kind, field, pattern, is_regex) VALUES (?, ?, ?, ?, ?)',
      )
      .run(feedId, kind, field, pattern, isRegex ? 1 : 0)
  }

  removeFilter(feedId: string, filterId: number): boolean {
    return (
      this.db.prepare('DELETE FROM filters WHERE feed_id = ? AND id = ?').run(feedId, filterId)
        .changes > 0
    )
  }

  removeAllFilters(feedId: string): void {
    this.db.prepare('DELETE FROM filters WHERE feed_id = ?').run(feedId)
  }

  // ---- seen articles ----

  getSeenKeys(feedId: string): Set<string> {
    const rows = this.db
      .prepare('SELECT article_key FROM articles_seen WHERE feed_id = ?')
      .all(feedId) as {
      article_key: string
    }[]
    return new Set(rows.map((r) => r.article_key))
  }

  markSeen(feedId: string, keys: string[], publishedAt: (number | null)[]): void {
    const insert = this.db.prepare(
      'INSERT OR IGNORE INTO articles_seen (feed_id, article_key, published_at) VALUES (?, ?, ?)',
    )
    const insertAll = this.db.transaction(() => {
      for (let i = 0; i < keys.length; i++) {
        insert.run(feedId, keys[i], publishedAt[i] ?? null)
      }
    })
    insertAll()
  }

  countSeen(feedId: string): number {
    const row = this.db
      .prepare('SELECT COUNT(*) AS n FROM articles_seen WHERE feed_id = ?')
      .get(feedId) as {
      n: number
    }
    return row.n
  }

  // ---- settings ----

  getSetting(key: string): string | null {
    const row = this.db.prepare('SELECT value FROM settings WHERE key = ?').get(key) as
      | { value: string | null }
      | undefined
    return row?.value ?? null
  }

  setSetting(key: string, value: string | null): void {
    this.db
      .prepare(
        'INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value',
      )
      .run(key, value)
  }

  getAllSettings(): Record<string, string | null> {
    const rows = this.db.prepare('SELECT key, value FROM settings').all() as {
      key: string
      value: string | null
    }[]
    return Object.fromEntries(rows.map((r) => [r.key, r.value]))
  }
}
