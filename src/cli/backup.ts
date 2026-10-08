// CLI: npm run backup [-- output.json]
import fs from 'node:fs'
import process from 'node:process'
import { exportBackup } from '../backup/export.js'
import { loadConfig, resolveDbPath } from '../config.js'
import { openDatabase } from '../db/database.js'
import { FeedStore } from '../db/feeds.js'

const config = loadConfig()
const output =
  process.argv[2] ?? `./data/backups/rrs-backup-${new Date().toISOString().slice(0, 10)}.json`
const db = openDatabase({ dbPath: resolveDbPath(config.dbPath) })
const backup = exportBackup(new FeedStore(db))
fs.mkdirSync(output.substring(0, output.lastIndexOf('/')) || '.', { recursive: true })
fs.writeFileSync(output, JSON.stringify(backup, null, 2))
db.close()
console.log(`Backup written to ${output} (${backup.feeds.length} feeds)`)
