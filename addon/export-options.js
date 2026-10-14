// Prints shell exports from the Home Assistant add-on options file.
// Output is eval'd by run.sh. Only known keys are exported.
const fs = require('node:fs')

const OPTION_TO_ENV = {
  discord_token: 'DISCORD_TOKEN',
  default_interval_minutes: 'DEFAULT_INTERVAL_MINUTES',
  send_delay_ms: 'SEND_DELAY_MS',
  log_level: 'LOG_LEVEL',
  backup_hour: 'BACKUP_HOUR',
  backup_retention_days: 'BACKUP_RETENTION_DAYS',
  timezone: 'TIMEZONE',
}

const path = '/data/options.json'
if (!fs.existsSync(path)) process.exit(0)

const options = JSON.parse(fs.readFileSync(path, 'utf-8'))
for (const [option, env] of Object.entries(OPTION_TO_ENV)) {
  const value = options[option]
  if (value === undefined || value === null || value === '') continue
  const safe = String(value).replace(/'/g, `'\\''`)
  console.log(`export ${env}='${safe}'`)
}
