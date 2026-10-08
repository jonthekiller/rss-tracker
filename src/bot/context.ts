import type { Client } from 'discord.js'
import type { Config } from '../config.js'
import type { Database } from '../db/database.js'
import type { FeedStore } from '../db/feeds.js'
import type { Pipeline, Scheduler } from '../scheduler/scheduler.js'
import type { SenderQueue } from '../sender/queue.js'

/**
 * Shared context handed to all interaction handlers.
 * Populated by bot.ts and mutated once the client is logged in.
 */
export interface BotContext {
  config: Config
  db: Database
  store: FeedStore
  sender: SenderQueue
  pipeline: Pipeline
  scheduler: Scheduler
  client: Client
}

/**
 * Placeholder for post-login wiring (reserved for future needs such as
 * cache warm-up). Kept as a hook so bot.ts stays linear.
 */
export function startBotContext(_context: BotContext): void {}
