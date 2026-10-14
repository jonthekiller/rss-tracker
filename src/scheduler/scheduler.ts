import type { FeedStore } from '../db/feeds.js'
import { detectNewArticles } from '../feed/detect.js'
import { fetchFeed } from '../feed/fetcher.js'
import type { Article } from '../feed/types.js'
import { buildDigestMessages } from '../render/digest.js'
import { buildArticlePayload } from '../render/embed.js'
import type { TemplateContext } from '../render/template.js'
import type { SenderQueue } from '../sender/queue.js'
import { createLogger } from '../util/logger.js'

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
  onFeedFailed?: (feed: {
    id: string
    name: string
    url: string
    channelId: string
    lastError: string | null
  }) => void
  /** Called when a previously failing feed succeeds again. */
  onFeedRecovered?: (feed: { id: string; name: string; url: string }) => void
  /** Consecutive failures before alerting. */
  failureAlertThreshold?: number
}

export const FAILURE_ALERT_THRESHOLD = 3
const MAX_BACKOFF_MS = 6 * 3600 * 1000

const log = createLogger('scheduler')

export interface DryRunReport {
  feed: {
    id: string
    name: string
    url: string
    channelId: string
    enabled: boolean
    pauseReason: string | null
    intervalMinutes: number
    batchMode: string
    batchThreshold: number
    templateText: string
    embedJson: string | null
  }
  state: {
    seenCount: number
    nextCheckAt: number
    consecutiveFailures: number
    lastError: string | null
    queueDepth: number
  }
  fetch?: {
    status: 'not_modified' | 'ok' | 'error'
    articleCount?: number
    feedTitle?: string | null
    etagReceived?: string | null
    lastModifiedReceived?: string | null
    detail?: string
    error?: string
  }
  detection?: {
    fresh: number
    blocked: number
    tooOld: number
    freshTitles: string[]
    blockedTitles: string[]
  }
  delivery?: {
    mode: string
    messages: number
    wouldSendNow: boolean
  }
  render?: {
    articleTitle: string
    content: string
    embedCount: number
  }
}

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
        log.debug({ feed: feed.name }, 'Not modified (304)')
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
      if (detection.fresh.length > 0) {
        log.info(
          {
            feed: feed.name,
            fresh: detection.fresh.length,
            blocked: detection.blocked.length,
            skipped: detection.tooOld.length,
          },
          'Feed checked with new articles',
        )
      }
      return {
        feedId,
        status: detection.fresh.length > 0 ? 'delivered' : 'empty',
        freshCount: detection.fresh.length,
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      log.warn({ feed: feed.name, url: feed.url, error: message }, 'Feed check failed')
      store.markFailure(feed.id, now, message)
      const updated = store.getFeed(feed.id)
      const failures = updated?.consecutiveFailures ?? 1
      const backoff = Math.min(
        MAX_BACKOFF_MS,
        this.options.maxBackoffMs ?? this.intervalMs(feed) * 2 ** failures,
      )
      store.setNextCheck(feed.id, now + backoff)
      // Per-channel threshold (/settings alert threshold) wins over the global one.
      const perChannelRaw = store.getChannelSetting(feed.channelId, 'alert_threshold')
      const perChannel = perChannelRaw === null ? NaN : Number.parseInt(perChannelRaw, 10)
      const threshold = Number.isNaN(perChannel)
        ? (this.options.failureAlertThreshold ?? FAILURE_ALERT_THRESHOLD)
        : perChannel
      if (failures >= threshold && !(updated?.failedNotified ?? false)) {
        this.options.onFeedFailed?.({
          id: feed.id,
          name: feed.name,
          url: feed.url,
          channelId: feed.channelId,
          lastError: updated?.lastError ?? message,
        })
      }
      return { feedId, status: 'error', error: message }
    }
  }

  /**
   * Fetch and analyse a feed WITHOUT changing any state (no baseline,
   * no next_check, no failure counters). Used by /debug feed.
   */
  async dryRun(
    feedId: string,
    now: number = this.options.now?.() ?? Date.now(),
  ): Promise<DryRunReport> {
    const store = this.options.store
    const feed = store.getFeed(feedId)
    if (!feed) throw new Error(`Feed ${feedId} not found`)

    const seenCount = store.countSeen(feed.id)
    const report: DryRunReport = {
      feed: {
        id: feed.id,
        name: feed.name,
        url: feed.url,
        channelId: feed.channelId,
        enabled: feed.enabled,
        pauseReason: feed.pauseReason,
        intervalMinutes: feed.intervalMinutes,
        batchMode: feed.batchMode,
        batchThreshold: feed.batchThreshold,
        templateText: feed.templateText,
        embedJson: feed.embedJson,
      },
      state: {
        seenCount,
        nextCheckAt: feed.nextCheckAt,
        consecutiveFailures: feed.consecutiveFailures,
        lastError: feed.lastError,
        queueDepth: this.options.sender.depth(feed.channelId),
      },
    }

    try {
      const result = await fetchFeed(feed.url, {
        etag: feed.etag,
        lastModified: feed.lastModified,
        fetchImpl: this.options.fetchImpl,
      })

      if (result.status === 'not_modified') {
        report.fetch = {
          status: 'not_modified',
          detail: 'HTTP 304 — the feed has not changed since the last check',
        }
        return report
      }

      const detection = detectNewArticles({
        articles: result.articles,
        knownKeys: store.getSeenKeys(feed.id),
        maxAgeDays: feed.maxAgeDays,
        now,
        filters: store.getFilters(feed.id),
      })
      const willDigest =
        feed.batchMode === 'digest' ||
        (feed.batchMode === 'auto' && detection.fresh.length >= feed.batchThreshold)

      report.fetch = {
        status: 'ok',
        articleCount: result.articles.length,
        feedTitle: result.meta.title,
        etagReceived: result.etag,
        lastModifiedReceived: result.lastModified,
      }
      report.detection = {
        fresh: detection.fresh.length,
        blocked: detection.blocked.length,
        tooOld: detection.tooOld.length,
        freshTitles: detection.fresh.slice(-5).map((a) => a.title),
        blockedTitles: detection.blocked.slice(0, 5).map((a) => a.title),
      }
      report.delivery = {
        mode: willDigest ? 'digest' : 'individual',
        messages: willDigest ? 1 : detection.fresh.length,
        wouldSendNow: feed.enabled && detection.fresh.length > 0 && seenCount > 0,
      }

      // Render preview of the latest fresh article (or the latest article).
      const previewArticle = detection.fresh.at(-1) ?? result.articles.at(-1)
      if (previewArticle) {
        const payload = buildArticlePayload(feed.templateText, feed.embedJson, {
          article: previewArticle,
          feedName: feed.name,
          timezone: this.options.timezone,
        })
        report.render = {
          articleTitle: previewArticle.title,
          content: payload.content,
          embedCount: payload.embeds.length,
        }
      }
      return report
    } catch (error) {
      report.fetch = {
        status: 'error',
        error: error instanceof Error ? error.message : String(error),
      }
      return report
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
      log.info(
        {
          feed: feed.name,
          channel: feed.channelId,
          fresh: fresh.length,
          messages: messages.length,
        },
        'Digest enqueued for burst',
      )
      return
    }

    for (const article of fresh) {
      const payload = buildArticlePayload(feed.templateText, feed.embedJson, context(article))
      this.options.sender.send(feed.channelId, payload)
    }
    log.info(
      { feed: feed.name, channel: feed.channelId, fresh: fresh.length },
      'Individual articles enqueued',
    )
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
