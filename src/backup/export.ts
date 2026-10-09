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

/**
 * Backup format v2: feeds and settings are grouped PER CHANNEL, matching the
 * channel-scoped permission model. Restoring a channel group requires write
 * access to that channel.
 */
export interface BackupChannel {
  channelId: string
  settings: Record<string, string | null>
  feeds: BackupFeed[]
}

export interface BackupFile {
  version: 2
  exportedAt: string
  channels: BackupChannel[]
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

/**
 * Export a backup. When channelIds is provided, only those channels are
 * exported (their feeds + their settings); otherwise every channel.
 */
export function exportBackup(store: FeedStore, channelIds?: string[]): BackupFile {
  const channels = channelIds ?? store.getAllChannelIds()
  const groups: BackupChannel[] = []
  for (const channelId of channels) {
    const feeds = store.getFeedsForChannel(channelId).map((feed) => feedToBackup(feed, store))
    const settings = store.getAllChannelSettings(channelId)
    if (feeds.length === 0 && Object.keys(settings).length === 0) continue
    groups.push({ channelId, settings, feeds })
  }
  return {
    version: 2,
    exportedAt: new Date().toISOString(),
    channels: groups,
  }
}

// ---------------------------------------------------------------------------
// Restore (v2 native, v1 MonitoRSS-style backups normalized)
// ---------------------------------------------------------------------------

export interface RestoreEntryResult {
  name: string
  url: string
  status: 'restored' | 'updated' | 'channel_missing' | 'forbidden' | 'error'
  detail?: string | undefined
}

export interface RestoreOptions {
  /** Guild the feeds belong to (Discord command context). */
  guildId: string
  /** Fallback channel when a backup channel does not exist in this guild. */
  fallbackChannelId?: string
  /** Map old channel IDs to new ones: { old: new }. */
  mapChannels?: Record<string, string>
  /** Returns true when a channel ID exists in the target guild. */
  channelExists: (guildId: string, channelId: string) => Promise<boolean>
  /** Whether the person running the restore may manage (write in) a channel. */
  actorCanManageChannel: (channelId: string) => Promise<boolean>
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
  validate?: (data: unknown) => BackupFile
}

/** Accept both v2 backups and legacy v1 (flat feeds, global settings). */
export function normalizeBackup(data: unknown): BackupFile {
  if (data === null || typeof data !== 'object') {
    throw new Error('Invalid backup: not a JSON object')
  }
  const raw = data as { version?: unknown; feeds?: unknown; channels?: unknown }
  if (raw.version === 2) {
    if (!Array.isArray(raw.channels)) {
      throw new Error('Invalid backup: missing channels array')
    }
    return data as BackupFile
  }
  if (raw.version === 1) {
    if (!Array.isArray(raw.feeds)) {
      throw new Error('Invalid backup: missing feeds array')
    }
    // v1: flat feed list with per-feed channelId; global settings dropped.
    const groups = new Map<
      string,
      { channelId: string; settings: Record<string, string | null>; feeds: unknown[] }
    >()
    for (const feed of raw.feeds as { channelId?: string; channel?: string }[]) {
      const channelId = feed.channelId ?? feed.channel
      if (!channelId) continue
      let group = groups.get(channelId)
      if (!group) {
        group = { channelId, settings: {}, feeds: [] }
        groups.set(channelId, group)
      }
      group.feeds.push(feed)
    }
    return {
      version: 2,
      exportedAt: new Date().toISOString(),
      channels: [...groups.values()] as BackupChannel[],
    }
  }
  throw new Error(`Unsupported backup version: ${String(raw.version)}`)
}

/**
 * Restore a backup file: upsert feeds by stable UUID, restore filters and
 * per-channel settings, and seen keys (no duplicates, no replayed bursts).
 * Each channel group requires the actor to manage that channel.
 */
export async function restoreBackup(
  store: FeedStore,
  data: unknown,
  options: RestoreOptions,
): Promise<RestoreEntryResult[]> {
  const backup = options.validate?.(data) ?? normalizeBackup(data)
  const results: RestoreEntryResult[] = []

  for (const group of backup.channels) {
    const targetChannel = await resolveChannel(options, group.channelId)
    if (!targetChannel) {
      for (const feedBackup of group.feeds) {
        results.push({
          name: feedBackup.name,
          url: feedBackup.url,
          status: 'channel_missing',
          detail: `Channel ${group.channelId} not found in this guild and no fallback provided`,
        })
      }
      continue
    }
    if (!(await options.actorCanManageChannel(targetChannel))) {
      for (const feedBackup of group.feeds) {
        results.push({
          name: feedBackup.name,
          url: feedBackup.url,
          status: 'forbidden',
          detail: `You need the Send Messages permission in <#${targetChannel}> to restore its feeds`,
        })
      }
      continue
    }

    // Per-channel settings.
    for (const [key, value] of Object.entries(group.settings ?? {})) {
      store.setChannelSetting(targetChannel, key, value)
    }

    for (const feedBackup of group.feeds) {
      await restoreFeed(store, options, feedBackup, targetChannel, results)
    }
  }

  return results
}

async function restoreFeed(
  store: FeedStore,
  options: RestoreOptions,
  feedBackup: BackupFeed,
  targetChannel: string,
  results: RestoreEntryResult[],
): Promise<void> {
  try {
    const existing = store.getFeed(feedBackup.id)
    if (existing) {
      // Existing feeds keep their live channel (never moved by a restore).
      store.updateFeed(feedBackup.id, {
        name: feedBackup.name,
        url: feedBackup.url,
        guildId: options.guildId,
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
    const feedChannel = existing?.channelId ?? targetChannel

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

    // Baseline fetch for feeds restored without seen keys.
    if (!feedBackup.seenKeys?.length) {
      const baselineResult = await options.baseline(feedBackup.id, feedBackup.url)
      if (!baselineResult.ok) {
        results.push({
          name: feedBackup.name,
          url: feedBackup.url,
          status: existing ? 'updated' : 'restored',
          detail: `Restored, but baseline fetch failed: ${baselineResult.error ?? 'unknown'}. It will retry automatically.`,
        })
        return
      }
      if (feedBackup.name === 'Unknown' && baselineResult.feedName) {
        store.updateFeed(feedBackup.id, { name: baselineResult.feedName })
      }
    }

    results.push({
      name: feedBackup.name,
      url: feedBackup.url,
      status: existing ? 'updated' : 'restored',
      detail: feedChannel !== feedBackup.channelId ? `Channel: <#${feedChannel}>` : undefined,
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

async function resolveChannel(
  options: RestoreOptions,
  backupChannelId: string,
): Promise<string | null> {
  const mapped = options.mapChannels?.[backupChannelId]
  if (mapped) return mapped
  if (await options.channelExists(options.guildId, backupChannelId)) {
    return backupChannelId
  }
  if (options.fallbackChannelId) return options.fallbackChannelId
  return null
}

/** Generate a stable UUID for new feeds. */
export function newFeedId(): string {
  return randomUUID()
}
