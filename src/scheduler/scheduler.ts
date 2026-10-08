import type { FeedStore } from '../db/feeds.js'
import { detectNewArticles } from '../feed/detect.js'
import { fetchFeed } from '../feed/fetcher.js'
import type { Article } from '../feed/types.js'
import { buildDigestMessages } from '../render/digest.js'
import { buildArticlePayload } from '../render/embed.js'
import type { TemplateContext } from '../render/template.js'
import type { SenderQueue } from '../sender/queue.js'

export interface PipelineOptions {
  store: FeedStore
  sender: SenderQueue
  timezone: string
  minIntervalMinutes: number
  /** Concurrency for parallel feed fetches. */
  concurrency?: number
  /** Max backoff in ms (default 6 h). */
  maxBackoffMs?: number
  fetchImpl?: typeof fetch
  now?: () => number
  /** Called when a feed crosses the failure alert threshold. */
  onFeedFailed?: (feed: { id: string; name: string; url: string; lastError: string | null }) => void
  /** Called when a previously failing feed succeeds again. */
  onFeedRecovered?: (feed: { id: string; name: string; url: string }) => void
  /** Consecutive failures before alerting. */
  failureAlertThreshold?: number
}

export const FAILURE_ALERT_THRESHOLD = 3
const MAX_BACKOFF_MS = 6 * 3600 * 1000

export interface CheckOutcome {
  feedId: string
  status: 'not_modified' | 'delivered' | 'empty' | 'error'
  freshCount?: number
  error?: string
}

export class Pipeline {
  constructor(private readonly options: PipelineOptions) {}

  intervalMs(feed: { intervalMinutes: number }): number {
    return Math.max(this.options.minIntervalMinutes, feed.intervalMinutes) * 60_000
  }

  /** Baseline a feed: mark every current article as seen without sending anything. */
  baseline(feed: { id: string }, articles: Article[], _now: number): void {
    this.options.store.markSeen(
      feed.id,
      articles.map((a) => a.key),
      articles.map((a) => a.publishedAt?.getTime() ?? null),
    )
  }

  async checkFeed(
    feedId: string,
    now: number = this.options.now?.() ?? Date.now(),
  ): Promise<CheckOutcome> {
    const store = this.options.store
    const feed = store.getFeed(feedId)
    if (!feed) return { feedId, status: 'error', error: 'feed not found' }

    const wasFailing = feed.consecutiveFailures > 0

    try {
      const result = await fetchFeed(feed.url, {
        etag: feed.etag,
        lastModified: feed.lastModified,
        fetchImpl: this.options.fetchImpl,
      })

      if (result.status === 'not_modified') {
        store.markChecked(feed.id, now, null, null)
        store.setNextCheck(feed.id, now + this.intervalMs(feed))
        if (wasFailing) this.options.onFeedRecovered?.(feed)
        return { feedId, status: 'not_modified' }
      }

      const knownKeys = store.getSeenKeys(feed.id)
      const detection = detectNewArticles({
        articles: result.articles,
        knownKeys,
        maxAgeDays: feed.maxAgeDays,
        now,
        filters: store.getFilters(feed.id),
      })

      // A feed without prior seen-state (first check ever) is baselined
      // without delivering: never flood a channel on the very first fetch.
      const hasPriorState = store.countSeen(feed.id) > 0

      // Mark every article currently in the feed as seen (including blocked
      // and too old ones) so they are never reprocessed.
      this.baseline(feed, result.articles, now)

      if (detection.fresh.length > 0 && hasPriorState) {
        this.deliver(feed, detection.fresh)
      }

      store.markChecked(feed.id, now, result.etag, result.lastModified)
      if (wasFailing) this.options.onFeedRecovered?.(feed)

      store.setNextCheck(feed.id, now + this.intervalMs(feed))
      return {
        feedId,
        status: detection.fresh.length > 0 ? 'delivered' : 'empty',
        freshCount: detection.fresh.length,
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      store.markFailure(feed.id, now, message)
      const updated = store.getFeed(feed.id)
      const failures = updated?.consecutiveFailures ?? 1
      const backoff = Math.min(
        MAX_BACKOFF_MS,
        this.options.maxBackoffMs ?? this.intervalMs(feed) * 2 ** failures,
      )
      store.setNextCheck(feed.id, now + backoff)
      if (
        failures >= (this.options.failureAlertThreshold ?? FAILURE_ALERT_THRESHOLD) &&
        !(updated?.failedNotified ?? false)
      ) {
        this.options.onFeedFailed?.({
          id: feed.id,
          name: feed.name,
          url: feed.url,
          lastError: updated?.lastError ?? message,
        })
      }
      return { feedId, status: 'error', error: message }
    }
  }

  /** Render and enqueue delivery of fresh articles (batch-aware). */
  deliver(
    feed: {
      id: string
      name: string
      channelId: string
      templateText: string
      embedJson: string | null
      batchMode: string
      batchThreshold: number
      batchMaxItems: number
    },
    fresh: Article[],
  ): void {
    const context = (article: Article): TemplateContext => ({
      article,
      feedName: feed.name,
      timezone: this.options.timezone,
    })

    const useDigest =
      feed.batchMode === 'digest' ||
      (feed.batchMode === 'auto' && fresh.length >= feed.batchThreshold)

    if (useDigest) {
      const messages = buildDigestMessages(
        fresh.map((a) => ({ title: a.title, link: a.link })),
        {
          feedName: feed.name,
          totalNew: fresh.length,
          maxItems: feed.batchMaxItems,
          timezone: this.options.timezone,
        },
      )
      for (const message of messages) {
        this.options.sender.send(feed.channelId, message)
      }
      return
    }

    for (const article of fresh) {
      const payload = buildArticlePayload(feed.templateText, feed.embedJson, context(article))
      this.options.sender.send(feed.channelId, payload)
    }
  }
}

export class Scheduler {
  private timer: NodeJS.Timeout | null = null
  private running = false
  /** Suspended during import/restore transactions. */
  private suspended = false

  constructor(
    private readonly pipeline: Pipeline,
    private readonly store: FeedStore,
    private readonly options: {
      tickIntervalMs?: number
      batchSize?: number
      now?: () => number
    } = {},
  ) {}

  /** Pause/resume processing (used by import & restore to run inside a transaction). */
  suspend(): void {
    this.suspended = true
  }

  resume(): void {
    this.suspended = false
  }

  isSuspended(): boolean {
    return this.suspended
  }

  start(): void {
    if (this.timer) return
    const tickIntervalMs = this.options.tickIntervalMs ?? 30_000
    this.timer = setInterval(() => {
      void this.tick()
    }, tickIntervalMs)
    this.timer.unref?.()
    // First tick right away (async, non-blocking).
    void this.tick()
  }

  stop(): void {
    if (this.timer) {
      clearInterval(this.timer)
      this.timer = null
    }
  }

  /** One processing pass; returns the number of feeds checked. */
  async tick(): Promise<number> {
    if (this.running || this.suspended) return 0
    this.running = true
    try {
      const now = this.options.now?.() ?? Date.now()
      const due = this.store.getFeedsDueNow(now, this.options.batchSize ?? 50)
      if (due.length === 0) return 0

      const concurrency = 4
      let index = 0
      const worker = async () => {
        while (index < due.length) {
          const feed = due[index]
          index += 1
          if (feed) await this.pipeline.checkFeed(feed.id, now)
        }
      }
      await Promise.all(Array.from({ length: Math.min(concurrency, due.length) }, worker))
      return due.length
    } finally {
      this.running = false
    }
  }
}
