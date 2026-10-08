import pino from 'pino'
import { getConfig } from '../config.js'

/** Log level from config when available, without throwing in tests/CLI. */
function resolveLevel(): string {
  try {
    return getConfig().logLevel
  } catch {
    return 'info'
  }
}

export function createLogger(scope: string) {
  return pino({
    level: process.env.LOG_LEVEL_OVERRIDE ?? resolveLevel(),
    base: { scope },
    // Human-readable container logs (docker logs / HA add-on logs)
    transport: {
      target: 'pino-pretty',
      options: {
        colorize: false,
        translateTime: 'SYS:yyyy-mm-dd HH:MM:ss.l',
        ignore: 'pid,hostname,scope',
        singleLine: true,
        messageFormat: '[{scope}] {msg}',
      },
    },
  })
}
