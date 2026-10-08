// CLI: npm run import -- monitorss-backup.json --guild <id> --channel <fallbackChannelId> [--map-old-channel old=new ...]
import fs from 'node:fs'
import process from 'node:process'
import { loadConfig, resolveDbPath } from '../config.js'
import { openDatabase } from '../db/database.js'
import { FeedStore } from '../db/feeds.js'
import { importMonitoRssBackup } from '../import/monitorss.js'
import { DEFAULT_TEMPLATE } from '../render/template.js'

const args = process.argv.slice(2)
const file = args[0]
if (!file) {
  console.error(
    'Usage: npm run import -- backup.json --guild <id> --channel <id> [--map-old-channel old=new ...]',
  )
  process.exit(1)
}
let guildId = ''
let fallbackChannelId = ''
const mapOldChannels: Record<string, string> = {}
for (let i = 1; i < args.length; i++) {
  const arg = args[i]
  if (arg === '--guild') guildId = args[++i] ?? ''
  else if (arg === '--channel') fallbackChannelId = args[++i] ?? ''
  else {
    const match = /^--map-old-channel=([^=]+)=(.+)$/.exec(arg ?? '')
    if (match?.[1] && match[2]) mapOldChannels[match[1]] = match[2]
  }
}
if (!guildId || !fallbackChannelId) {
  console.error('--guild and --channel are required')
  process.exit(1)
}

const config = loadConfig()
const db = openDatabase({ dbPath: resolveDbPath(config.dbPath) })
const store = new FeedStore(db)
const data: unknown = JSON.parse(fs.readFileSync(file, 'utf-8'))

const results = await importMonitoRssBackup(store, data, {
  guildId,
  fallbackChannelId,
  mapOldChannels,
  channelExists: () => Promise.resolve(false), // CLI cannot verify; map explicitly
  defaultTemplate: DEFAULT_TEMPLATE,
})

for (const r of results) {
  console.log(`${r.status.toUpperCase().padEnd(10)} ${r.name} — ${r.detail ?? ''}`)
}
db.close()
