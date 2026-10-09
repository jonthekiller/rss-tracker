import { createRequire } from 'node:module'
import path from 'node:path'
import {
  Client,
  Events,
  GatewayIntentBits,
  type Interaction,
  type NewsChannel,
  type TextChannel,
  type ThreadChannel,
} from 'discord.js'
import { startAutoBackupLoop } from '../backup/auto.js'
import { getConfig, resolveDbPath } from '../config.js'
import { kvSet, openDatabase } from '../db/database.js'
import { FeedStore } from '../db/feeds.js'
import { Pipeline, Scheduler } from '../scheduler/scheduler.js'
import { SenderQueue } from '../sender/queue.js'
import { createLogger } from '../util/logger.js'
import { handleInteraction, registerCommands } from './commands.js'
import { type BotContext, startBotContext } from './context.js'

const log = createLogger('bot')

const require = createRequire(import.meta.url)
const { version } = require('../../package.json') as { version: string }

export async function startBot(): Promise<void> {
  const config = getConfig()
  const db = openDatabase({ dbPath: resolveDbPath(config.dbPath) })
  const store = new FeedStore(db)

  const client = new Client({
    intents: [GatewayIntentBits.Guilds],
  })

  const sender = new SenderQueue({
    sendDelayMs: config.sendDelayMs,
    send: async (channelId, message) => {
      const channel = await client.channels.fetch(channelId)
      if (!channel?.isSendable()) {
        throw Object.assign(new Error('Channel not found or not sendable'), { code: 10003 })
      }
      await channel.send({ content: message.content, embeds: message.embeds })
    },
    onSendError: (channelId, _message, error) => {
      const code = (error as { code?: number }).code
      log.warn({ channelId, code, err: error }, 'Failed to send message to channel')
      if (code === 10003 || code === 50001 || code === 50013) {
        // Unknown channel / missing access: pause every feed targeting it.
        const feeds = store.getFeedsForChannel(channelId)
        for (const feed of feeds) {
          if (feed.enabled) {
            store.updateFeed(feed.id, { enabled: false, pauseReason: 'channel inaccessible' })
          }
        }
      }
    },
  })

  const pipeline = new Pipeline({
    store,
    sender,
    timezone: config.timezone,
    minIntervalMinutes: config.minIntervalMinutes,
    onFeedFailed: (feed) => {
      void sendFailureAlert(client, store, feed)
    },
    onFeedRecovered: () => {},
  })

  const scheduler = new Scheduler(pipeline, store)

  const context: BotContext = {
    config,
    version,
    db,
    store,
    sender,
    pipeline,
    scheduler,
    client,
  }

  client.once(Events.ClientReady, async (readyClient) => {
    log.info({ user: readyClient.user.tag, guilds: readyClient.guilds.cache.size }, 'Bot ready')
    try {
      await registerCommands(readyClient)
      log.info('Slash commands registered')
    } catch (error) {
      log.error({ err: error }, 'Failed to register slash commands')
    }
  })

  client.on(Events.InteractionCreate, (interaction: Interaction) => {
    void handleInteraction(context, interaction).catch((error) => {
      log.error({ err: error }, 'Interaction handler error')
    })
  })

  // Heartbeat for the Docker healthcheck.
  setInterval(() => {
    kvSet(db, 'last_scheduler_tick', new Date().toISOString())
  }, 30_000).unref?.()

  const stopAutoBackup = startAutoBackupLoop({
    store,
    directory: `${path.dirname(resolveDbPath(config.dbPath))}/backups`,
    hour: config.backupHour,
  })

  await client.login(config.discordToken)
  startBotContext(context)

  scheduler.start()
  log.info({ feeds: store.getAllFeeds().length }, 'Scheduler started')

  const shutdown = async () => {
    log.info('Shutting down')
    scheduler.stop()
    stopAutoBackup()
    client.destroy()
    db.close()
    process.exit(0)
  }
  process.on('SIGINT', () => void shutdown())
  process.on('SIGTERM', () => void shutdown())
}

async function sendFailureAlert(
  client: Client,
  store: FeedStore,
  feed: { id: string; name: string; url: string; channelId: string; lastError: string | null },
): Promise<void> {
  const message = `⚠️ **Feed failed** — ${feed.name} (<${feed.url}>)\n\`\`\`\n${feed.lastError ?? 'unknown error'}\n\`\`\``
  try {
    // Alert targets are configured per channel (/settings alert in that channel).
    const userId = store.getChannelSetting(feed.channelId, 'alert_user_id')
    if (userId) {
      const user = await client.users.fetch(userId)
      await user.send(message)
      store.updateFeed(feed.id, { failedNotified: true })
      return
    }
    const channelId = store.getChannelSetting(feed.channelId, 'alert_channel_id')
    if (channelId) {
      const channel = await client.channels.fetch(channelId)
      if (channel?.isSendable()) {
        await channel.send(message)
        store.updateFeed(feed.id, { failedNotified: true })
      }
    }
  } catch (error) {
    log.error({ err: error, feedId: feed.id }, 'Failed to send failure alert')
  }
}

export type { NewsChannel, TextChannel, ThreadChannel }
