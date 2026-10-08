// CLI: npm run restore -- file.json [--map-channel old=new ...]
import fs from 'node:fs'
import process from 'node:process'
import { restoreBackup } from '../backup/export.js'
import { loadConfig, resolveDbPath } from '../config.js'
import { openDatabase } from '../db/database.js'
import { FeedStore } from '../db/feeds.js'
import { fetchFeed } from '../feed/fetcher.js'
import { Pipeline } from '../scheduler/scheduler.js'
import { SenderQueue } from '../sender/queue.js'

const args = process.argv.slice(2)
const file = args[0]
if (!file) {
  console.error('Usage: npm run restore -- file.json [--map-channel old=new ...]')
  process.exit(1)
}
const mapChannels: Record<string, string> = {}
for (let i = 1; i < args.length; i++) {
  const match = /^--map-channel=([^=]+)=(.+)$/.exec(args[i] ?? '')
  if (match?.[1] && match[2]) mapChannels[match[1]] = match[2]
}

const config = loadConfig()
const db = openDatabase({ dbPath: resolveDbPath(config.dbPath) })
const store = new FeedStore(db)
const pipeline = new Pipeline({
  store,
  sender: new SenderQueue({ sendDelayMs: config.sendDelayMs, send: async () => {} }),
  timezone: config.timezone,
  minIntervalMinutes: config.minIntervalMinutes,
})

const data: unknown = JSON.parse(fs.readFileSync(file, 'utf-8'))
const guildId = process.env.RESTORE_GUILD_ID ?? ''
if (!guildId) {
  console.error('RESTORE_GUILD_ID env var is required for CLI restore')
  process.exit(1)
}

const results = await restoreBackup(store, data, {
  guildId,
  mapChannels,
  channelExists: () => Promise.resolve(false), // CLI cannot verify; remap explicitly
  baseline: async (feedId, url) => {
    try {
      const result = await fetchFeed(url)
      if (result.status === 'ok') {
        pipeline.baseline({ id: feedId }, result.articles, Date.now())
        return { ok: true, feedName: result.meta.title ?? undefined }
      }
      return { ok: true }
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : String(error) }
    }
  },
})

for (const r of results) {
  console.log(`${r.status.toUpperCase().padEnd(16)} ${r.name} — ${r.detail ?? ''}`)
}
db.close()
