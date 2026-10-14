import fs from 'node:fs'
import path from 'node:path'
import type { FeedStore } from '../db/feeds.js'
import { type BackupFile, exportBackup } from './export.js'

export interface AutoBackupOptions {
  store: FeedStore
  directory: string
  hour: number | null
  /** Number of backup files to keep (default 7). */
  keep?: number
  now?: () => number
}

/** Write a dated backup file to the backup directory, keeping the most recent ones. */
export function writeAutoBackup(options: AutoBackupOptions): string | null {
  if (options.hour === null) return null
  const backup = exportBackup(options.store)
  fs.mkdirSync(options.directory, { recursive: true })
  const filename = `rss-backup-${isoDate(options.now?.() ?? Date.now())}.json`
  const filePath = path.join(options.directory, filename)
  fs.writeFileSync(filePath, JSON.stringify(backup, null, 2))
  rotate(options.directory, options.keep ?? 7)
  return filePath
}

/**
 * Daily auto-backup loop: checks every 10 minutes whether the configured hour
 * has been reached and today's backup has not been written yet.
 */
export function startAutoBackupLoop(options: AutoBackupOptions): () => void {
  let lastWrittenDate: string | null = null
  // Apply retention immediately at startup, so lowering the keep count (or
  // deleting the backup setting) cleans up stale files without waiting a day.
  if (options.hour !== null && fs.existsSync(options.directory)) {
    rotate(options.directory, options.keep ?? 7)
  }
  const timer = setInterval(() => {
    if (options.hour === null) return
    const nowMs = options.now?.() ?? Date.now()
    const date = new Date(nowMs)
    const today = isoDate(nowMs)
    if (date.getHours() < options.hour) return
    if (lastWrittenDate === today) return
    const filePath = writeAutoBackup(options)
    if (filePath) lastWrittenDate = today
  }, 10 * 60_000)
  timer.unref?.()
  return () => clearInterval(timer)
}

function isoDate(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10)
}

function rotate(directory: string, keep: number): void {
  const files = fs
    .readdirSync(directory)
    .filter((f) => f.startsWith('rss-backup-') && f.endsWith('.json'))
    .sort()
  for (const file of files.slice(0, Math.max(0, files.length - keep))) {
    fs.rmSync(path.join(directory, file), { force: true })
  }
}

export type { BackupFile }
