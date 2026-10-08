import pino from 'pino'
import { getConfig } from '../config.js'

export function createLogger(scope: string) {
  return pino({ level: getConfig().logLevel, base: { scope } })
}
