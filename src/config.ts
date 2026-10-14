import path from 'node:path'
import process from 'node:process'

export interface Config {
  discordToken: string
  dbPath: string
  defaultIntervalMinutes: number
  minIntervalMinutes: number
  sendDelayMs: number
  logLevel: string
  backupHour: number | null
  backupKeep: number
  failureAlertThreshold: number
  timezone: string
}

function intEnv(name: string, fallback: number): number {
  const raw = process.env[name]
  if (raw === undefined || raw === '') return fallback
  const value = Number.parseInt(raw, 10)
  return Number.isNaN(value) ? fallback : value
}

function boolEnv(name: string, fallback: boolean): boolean {
  const raw = process.env[name]
  if (raw === undefined || raw === '') return fallback
  return raw === '1' || raw.toLowerCase() === 'true'
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const discordToken = env.DISCORD_TOKEN ?? ''
  if (!discordToken) {
    throw new Error('DISCORD_TOKEN is required (see .env.example)')
  }

  const backupHourRaw = env.BACKUP_HOUR ?? ''
  const backupHour = backupHourRaw === '' ? null : Number.parseInt(backupHourRaw, 10)

  return {
    discordToken,
    dbPath: env.DB_PATH ?? './data/rss-tracker.db',
    defaultIntervalMinutes: Math.max(2, intEnv('DEFAULT_INTERVAL_MINUTES', 10)),
    minIntervalMinutes: Math.max(1, intEnv('MIN_INTERVAL_MINUTES', 2)),
    sendDelayMs: Math.max(250, intEnv('SEND_DELAY_MS', 1000)),
    logLevel: env.LOG_LEVEL ?? 'info',
    backupHour:
      backupHour === null || Number.isNaN(backupHour) || !boolEnv('BACKUP_ENABLED', true)
        ? null
        : Math.min(23, Math.max(0, backupHour)),
    backupKeep: Math.min(365, Math.max(1, intEnv('BACKUP_RETENTION_DAYS', 7))),
    failureAlertThreshold: Math.min(100, Math.max(1, intEnv('FAILURE_ALERT_THRESHOLD', 3))),
    timezone: env.TIMEZONE ?? 'UTC',
  }
}

export function resolveDbPath(dbPath: string): string {
  return path.isAbsolute(dbPath) ? dbPath : path.resolve(process.cwd(), dbPath)
}

let cached: Config | null = null

export function getConfig(): Config {
  if (!cached) {
    cached = loadConfig()
  }
  return cached
}

/** For tests only. */
export function resetConfigCache(): void {
  cached = null
}
