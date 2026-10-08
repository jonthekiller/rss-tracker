// Docker HEALTHCHECK: verify the database opens and the scheduler heartbeat is recent.
import fs from 'node:fs'
import process from 'node:process'
import { loadConfig, resolveDbPath } from './config.js'
import { kvGet, openDatabase } from './db/database.js'

const MAX_AGE_MS = 10 * 60_000

try {
  const config = loadConfig()
  const dbPath = resolveDbPath(config.dbPath)
  if (!fs.existsSync(dbPath)) {
    // First boot, database not created yet: not an error.
    process.exit(0)
  }
  const db = openDatabase({ dbPath })
  const lastTick = kvGet(db, 'last_scheduler_tick')
  db.close()
  if (!lastTick) process.exit(0) // bot may still be starting
  const age = Date.now() - new Date(lastTick).getTime()
  if (age > MAX_AGE_MS) {
    console.error(`Scheduler heartbeat too old: ${age} ms`)
    process.exit(1)
  }
  process.exit(0)
} catch (error) {
  console.error(error)
  process.exit(1)
}
