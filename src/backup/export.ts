import { randomUUID } from 'node:crypto'
import type { Feed, FeedStore } from '../db/feeds.js'

export interface BackupFeed {
  id: string
  name: string
  url: string
  guildId: string
  channelId: string
  enabled: boolean
  intervalMinutes: number
  templateText: string
  embedJson: string | null
  batchMode: string
  batchThreshold: number
  batchMaxItems: number
  maxAgeDays: number
  pauseReason: string | null
  filters: { kind: string; field: string; pattern: string; isRegex: boolean }[]
  seenKeys: string[]
}

export interface BackupFile {
  version: 1
  exportedAt: string
  settings: Record<string, string | null>
  feeds: BackupFeed[]
}

export function feedToBackup(feed: Feed, store: FeedStore): BackupFeed {
  return {
    id: feed.id,
    name: feed.name,
    url: feed.url,
    guildId: feed.guildId,
    channelId: feed.channelId,
    enabled: feed.enabled,
    intervalMinutes: feed.intervalMinutes,
    templateText: feed.templateText,
    embedJson: feed.embedJson,
    batchMode: feed.batchMode,
    batchThreshold: feed.batchThreshold,
    batchMaxItems: feed.batchMaxItems,
    maxAgeDays: feed.maxAgeDays,
    pauseReason: feed.pauseReason,
    filters: store.getFilters(feed.id).map((f) => ({
      kind: f.kind,
      field: f.field,
      pattern: f.pattern,
      isRegex: f.isRegex,
    })),
    seenKeys: [...store.getSeenKeys(feed.id)],
  }
}

export function exportBackup(store: FeedStore, _botName: string | null = null): BackupFile {
  const feeds = store.getAllFeeds().map((feed) => feedToBackup(feed, store))
  return {
    version: 1,
    exportedAt: new Date().toISOString(),
    settings: store.getAllSettings(),
    feeds,
  }
}

export interface RestoreEntryResult {
  name: string
  url: string
  status: 'restored' | 'updated' | 'channel_missing' | 'error'
  detail?: string | undefined
}

export interface RestoreOptions {
  /** Guild the feeds belong to (Discord command context). */
  guildId: string
  /** Fallback channel when the backup's channel does not exist in this guild. */
  fallbackChannelId?: string
  /** Map old channel IDs to new ones: { old: new }. */
  mapChannels?: Record<string, string>
  /** Returns true when a channel ID exists in the target guild. */
  channelExists: (guildId: string, channelId: string) => Promise<boolean>
  /** Baseline fetch: mark current articles as seen without sending. */
  baseline: (
    feedId: string,
    url: string,
  ) => Promise<{
    ok: boolean
    feedName?: string | undefined
    error?: string | undefined
  }>
  /** Called with the parsed backup before writing; may throw to abort. */
  validate?: (backup: unknown) => BackupFile
}

export function defaultValidate(data: unknown): BackupFile {
  if (data === null || typeof data !== 'object') {
    throw new Error('Invalid backup: not a JSON object')
  }
  const backup = data as Partial<BackupFile>
  if (backup.version !== 1) {
    throw new Error(
      `Unsupported backup version: ${String((backup as { version?: unknown }).version)}`,
    )
  }
  if (!Array.isArray(backup.feeds)) {
    throw new Error('Invalid backup: missing feeds array')
  }
  return backup as BackupFile
}

/**
 * Restore a backup file: upsert feeds by stable UUID, restore filters,
 * settings and seen keys (no duplicates, no replayed bursts).
 */
export async function restoreBackup(
  store: FeedStore,
  data: unknown,
  options: RestoreOptions,
): Promise<RestoreEntryResult[]> {
  const backup = options.validate?.(data) ?? defaultValidate(data)
  const results: RestoreEntryResult[] = []

  for (const settings of Object.entries(backup.settings ?? {})) {
    store.setSetting(settings[0], settings[1])
  }

  for (const feedBackup of backup.feeds) {
    const targetChannel = await resolveChannel(options, feedBackup)
    if (!targetChannel) {
      results.push({
        name: feedBackup.name,
        url: feedBackup.url,
        status: 'channel_missing',
        detail: `Channel ${feedBackup.channelId} not found in this guild and no fallback provided`,
      })
      continue
    }

    try {
      const existing = store.getFeed(feedBackup.id)
      if (existing) {
        store.updateFeed(feedBackup.id, {
          name: feedBackup.name,
          url: feedBackup.url,
          guildId: options.guildId,
          channelId: targetChannel,
          enabled: feedBackup.enabled,
          intervalMinutes: feedBackup.intervalMinutes,
          templateText: feedBackup.templateText,
          embedJson: feedBackup.embedJson,
          batchMode: feedBackup.batchMode as Feed['batchMode'],
          batchThreshold: feedBackup.batchThreshold,
          batchMaxItems: feedBackup.batchMaxItems,
          maxAgeDays: feedBackup.maxAgeDays,
          pauseReason: feedBackup.pauseReason,
        })
      } else {
        store.addFeed({
          id: feedBackup.id,
          name: feedBackup.name,
          url: feedBackup.url,
          guildId: options.guildId,
          channelId: targetChannel,
          enabled: feedBackup.enabled,
          intervalMinutes: feedBackup.intervalMinutes,
          templateText: feedBackup.templateText,
          embedJson: feedBackup.embedJson,
          batchMode: feedBackup.batchMode as Feed['batchMode'],
          batchThreshold: feedBackup.batchThreshold,
          batchMaxItems: feedBackup.batchMaxItems,
          maxAgeDays: feedBackup.maxAgeDays,
          pauseReason: feedBackup.pauseReason,
        })
      }

      // Filters: replace wholesale.
      store.removeAllFilters(feedBackup.id)
      for (const filter of feedBackup.filters ?? []) {
        store.addFilter(
          feedBackup.id,
          filter.kind as 'include' | 'exclude',
          filter.field as 'title' | 'description' | 'any',
          filter.pattern,
          filter.isRegex,
        )
      }

      // Seen keys: restore to avoid re-sending old articles.
      if (feedBackup.seenKeys?.length) {
        store.markSeen(
          feedBackup.id,
          feedBackup.seenKeys,
          feedBackup.seenKeys.map(() => null),
        )
      }

      // Baseline fetch for feeds restored without seen keys (or with none stored).
      if (!feedBackup.seenKeys?.length) {
        const baselineResult = await options.baseline(feedBackup.id, feedBackup.url)
        if (!baselineResult.ok) {
          results.push({
            name: feedBackup.name,
            url: feedBackup.url,
            status: 'restored',
            detail: `Restored, but baseline fetch failed: ${baselineResult.error ?? 'unknown'}. It will retry automatically.`,
          })
          continue
        }
        if (feedBackup.name === 'Unknown' && baselineResult.feedName) {
          store.updateFeed(feedBackup.id, { name: baselineResult.feedName })
        }
      }

      results.push({
        name: feedBackup.name,
        url: feedBackup.url,
        status: existing ? 'updated' : 'restored',
        detail:
          targetChannel !== feedBackup.channelId
            ? `Channel remapped to ${targetChannel}`
            : undefined,
      })
    } catch (error) {
      results.push({
        name: feedBackup.name,
        url: feedBackup.url,
        status: 'error',
        detail: error instanceof Error ? error.message : String(error),
      })
    }
  }

  return results
}

async function resolveChannel(
  options: RestoreOptions,
  feedBackup: BackupFeed,
): Promise<string | null> {
  const mapped = options.mapChannels?.[feedBackup.channelId]
  if (mapped) return mapped
  if (await options.channelExists(options.guildId, feedBackup.channelId)) {
    return feedBackup.channelId
  }
  if (options.fallbackChannelId) return options.fallbackChannelId
  return null
}

/** Generate a stable UUID for new feeds. */
export function newFeedId(): string {
  return randomUUID()
}
