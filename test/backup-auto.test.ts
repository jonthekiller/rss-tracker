import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { writeAutoBackup } from '../src/backup/auto.js'
import type { Database } from '../src/db/database.js'
import { openDatabase } from '../src/db/database.js'
import { FeedStore } from '../src/db/feeds.js'

const NOW = Date.parse('2026-01-10T12:00:00Z')

describe('writeAutoBackup retention', () => {
  let dir: string
  let db: InstanceType<typeof Database.DatabaseSync>
  let store: FeedStore

  beforeEach(() => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), 'rss-backup-'))
    db = openDatabase({ dbPath: ':memory:', ensureDir: false })
    store = new FeedStore(db)
  })

  afterEach(() => {
    fs.rmSync(dir, { recursive: true, force: true })
    db.close()
  })

  function seedOldBackups(count: number): void {
    for (let i = 0; i < count; i++) {
      const day = String(i + 1).padStart(2, '0')
      fs.writeFileSync(path.join(dir, `rss-backup-2025-12-${day}.json`), '{}')
    }
  }

  function listBackups(): string[] {
    return fs
      .readdirSync(dir)
      .filter((f) => f.startsWith('rss-backup-') && f.endsWith('.json'))
      .sort()
  }

  it('keeps the configured number of files, deleting the oldest first', () => {
    seedOldBackups(5)
    const result = writeAutoBackup({ store, directory: dir, hour: 3, keep: 3, now: () => NOW })
    expect(result).toContain('rss-backup-2026-01-10.json')
    expect(listBackups()).toEqual([
      'rss-backup-2025-12-04.json',
      'rss-backup-2025-12-05.json',
      'rss-backup-2026-01-10.json',
    ])
  })

  it('defaults to keeping 7 files', () => {
    seedOldBackups(9)
    writeAutoBackup({ store, directory: dir, hour: 3, now: () => NOW })
    expect(listBackups().length).toBe(7)
  })

  it('writes nothing when backups are disabled (hour null)', () => {
    expect(writeAutoBackup({ store, directory: dir, hour: null, keep: 3 })).toBeNull()
    seedOldBackups(2)
    expect(writeAutoBackup({ store, directory: dir, hour: null, keep: 3 })).toBeNull()
    expect(listBackups().length).toBe(2) // untouched
  })
})
