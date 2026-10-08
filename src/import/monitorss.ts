import { newFeedId } from '../backup/export.js'
import type { FeedStore } from '../db/feeds.js'
import { fetchFeed } from '../feed/fetcher.js'

/**
 * MonitoRSS backup (subset of fields we consume).
 * filters: include-filters by field; rfilters: regex include-filters.
 */
export interface MonitoRssFeed {
  title?: string
  url?: string
  guild?: string
  channel?: string
  text?: string
  embeds?: unknown[]
  filters?: Record<string, string[]>
  rfilters?: Record<string, string[]>
  checkDates?: boolean
}

export interface MonitoRssBackup {
  feeds?: MonitoRssFeed[]
  profile?: { name?: string; alert?: string[] }
  filteredFormats?: unknown[]
  subscribers?: unknown[]
}

export interface ImportEntryResult {
  name: string
  url: string
  status: 'imported' | 'skipped' | 'error'
  detail?: string
}

export interface ImportMonitorssOptions {
  guildId: string
  /** Destination channel: used when the backup's channel is not available. */
  fallbackChannelId: string
  /** Map old channel IDs to new ones: { old: new }. */
  mapOldChannels?: Record<string, string>
  /** Returns true when a channel ID exists in the target guild. */
  channelExists: (guildId: string, channelId: string) => Promise<boolean>
  /** Default template when the feed has none (MonitoRSS uses its own default). */
  defaultTemplate: string
  /** Injectable fetch for tests. */
  fetchImpl?: typeof fetch | undefined
  now?: () => number
}

export function parseMonitoRssBackup(data: unknown): MonitoRssBackup {
  if (data === null || typeof data !== 'object') {
    throw new Error('Invalid backup: not a JSON object')
  }
  const backup = data as MonitoRssBackup
  if (!Array.isArray(backup.feeds)) {
    throw new Error('Invalid MonitoRSS backup: missing "feeds" array')
  }
  return backup
}

/**
 * Import a MonitoRSS backup. Templates and include filters are preserved;
 * every feed gets a baseline fetch (existing articles marked seen, nothing sent).
 */
export async function importMonitoRssBackup(
  store: FeedStore,
  data: unknown,
  options: ImportMonitorssOptions,
): Promise<ImportEntryResult[]> {
  const backup = parseMonitoRssBackup(data)
  const results: ImportEntryResult[] = []
  const feeds = backup.feeds ?? []

  for (const rawFeed of feeds) {
    const name = rawFeed.title?.trim() || 'Unknown'
    const url = rawFeed.url?.trim()
    if (!url) {
      results.push({ name, url: '(missing)', status: 'skipped', detail: 'No URL in backup entry' })
      continue
    }
    if (
      store.exists(url, options.fallbackChannelId) &&
      !options.mapOldChannels?.[rawFeed.channel ?? '']
    ) {
      results.push({
        name,
        url,
        status: 'skipped',
        detail: 'This URL already exists for the target channel',
      })
      continue
    }

    try {
      const channelId = await resolveChannel(options, rawFeed)
      if (!channelId) {
        results.push({
          name,
          url,
          status: 'skipped',
          detail: `Channel ${rawFeed.channel} not found and no fallback`,
        })
        continue
      }

      const templateText = rawFeed.text?.trim() ? rawFeed.text : options.defaultTemplate
      const feed = store.addFeed({
        id: newFeedId(),
        name,
        url,
        guildId: options.guildId,
        channelId,
        templateText,
        embedJson:
          Array.isArray(rawFeed.embeds) && rawFeed.embeds.length > 0
            ? JSON.stringify(rawFeed.embeds[0])
            : null,
      })

      // MonitoRSS include filters (plain + regex).
      for (const [field, patterns] of Object.entries(rawFeed.filters ?? {})) {
        for (const pattern of patterns) {
          store.addFilter(feed.id, 'include', toField(field), pattern, false)
        }
      }
      for (const [field, patterns] of Object.entries(rawFeed.rfilters ?? {})) {
        for (const pattern of patterns) {
          store.addFilter(feed.id, 'include', toField(field), pattern, true)
        }
      }

      // Baseline: fetch now, mark all current articles seen, never send.
      try {
        const result = await fetchFeed(url, { fetchImpl: options.fetchImpl })
        if (result.status === 'ok') {
          store.markSeen(
            feed.id,
            result.articles.map((a) => a.key),
            result.articles.map((a) => a.publishedAt?.getTime() ?? null),
          )
          if (name === 'Unknown' && result.meta.title) {
            store.updateFeed(feed.id, { name: result.meta.title })
          }
        }
      } catch (error) {
        store.markFailure(
          feed.id,
          options.now?.() ?? Date.now(),
          error instanceof Error ? error.message : String(error),
        )
        results.push({
          name,
          url,
          status: 'imported',
          detail: `Imported, but first fetch failed: ${error instanceof Error ? error.message : String(error)}`,
        })
        continue
      }

      results.push({ name, url, status: 'imported' })
    } catch (error) {
      results.push({
        name,
        url,
        status: 'error',
        detail: error instanceof Error ? error.message : String(error),
      })
    }
  }

  return results
}

async function resolveChannel(
  options: ImportMonitorssOptions,
  feed: MonitoRssFeed,
): Promise<string | null> {
  const mapped = options.mapOldChannels?.[feed.channel ?? '']
  if (mapped) return mapped
  if (feed.channel && (await options.channelExists(options.guildId, feed.channel))) {
    return feed.channel
  }
  return options.fallbackChannelId
}

function toField(field: string): 'title' | 'description' | 'any' {
  if (field === 'title') return 'title'
  if (field === 'description') return 'description'
  return 'any'
}
