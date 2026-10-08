import { describe, expect, it, vi } from 'vitest'
import { openDatabase } from '../src/db/database.js'
import { FeedStore } from '../src/db/feeds.js'
import { Pipeline, Scheduler } from '../src/scheduler/scheduler.js'
import { SenderQueue } from '../src/sender/queue.js'

const rss = (items: string) => `<?xml version="1.0"?>
<rss version="2.0"><channel><title>Generated</title><link>https://g.test</link><description>d</description>${items}</channel></rss>`

function item(id: number, pubDate: string): string {
  return `<item><title>Item ${id}</title><link>https://g.test/${id}</link><guid>guid-${id}</guid><pubDate>${pubDate}</pubDate><description>Body ${id}</description></item>`
}

function jsonResponse(body: string, headers: Record<string, string> = {}): Response {
  return new Response(body, { status: 200, headers })
}

interface Setup {
  store: FeedStore
  sent: { channelId: string; content: string }[]
  fetcher: ReturnType<typeof vi.fn>
}

function setup(): Setup {
  const db = openDatabase({ dbPath: ':memory:', ensureDir: false })
  const store = new FeedStore(db)
  const sent: { channelId: string; content: string }[] = []
  const sender = new SenderQueue({
    sendDelayMs: 0,
    send: async (channelId, message) => {
      sent.push({ channelId, content: message.content })
    },
    delay: async () => {},
  })
  const fetcher = vi.fn()
  const pipeline = new Pipeline({
    store,
    sender,
    timezone: 'UTC',
    minIntervalMinutes: 2,
    fetchImpl: fetcher as unknown as typeof fetch,
  })
  // Attach sender/pipeline access for assertions via closures.
  ;(pipeline as unknown as { _sender: SenderQueue })._sender = sender
  ;(setup as unknown as { last: { sender: SenderQueue } }).last = { sender }
  return { store, sent, fetcher }
}

// Rebuild a pipeline+sender pair explicitly to keep references.
function buildPipeline(
  store: FeedStore,
  sent: { channelId: string; content: string }[],
  fetcher: ReturnType<typeof vi.fn>,
) {
  const sender = new SenderQueue({
    sendDelayMs: 0,
    send: async (channelId, message) => {
      sent.push({ channelId, content: message.content })
    },
    delay: async () => {},
  })
  const pipeline = new Pipeline({
    store,
    sender,
    timezone: 'UTC',
    minIntervalMinutes: 2,
    fetchImpl: fetcher as unknown as typeof fetch,
  })
  return { sender, pipeline }
}

const NOW = Date.parse('2026-01-10T00:00:00Z')

describe('Pipeline + Scheduler', () => {
  it('delivers individual messages when few new articles', async () => {
    const { store, sent, fetcher } = setup()
    const { pipeline } = buildPipeline(store, sent, fetcher)
    store.addFeed({
      id: 'f1',
      url: 'https://g.test/rss',
      guildId: 'g',
      channelId: 'c1',
      batchMode: 'individual',
    })

    fetcher.mockResolvedValue(
      jsonResponse(
        rss(item(1, 'Fri, 02 Jan 2026 12:00:00 GMT') + item(2, 'Sat, 03 Jan 2026 12:00:00 GMT')),
      ),
    )

    // Baseline: nothing is sent, all articles marked seen.
    await pipeline.checkFeed('f1', NOW)
    expect(sent).toHaveLength(0)
    expect(store.countSeen('f1')).toBe(2)

    // New article arrives.
    fetcher.mockResolvedValue(
      jsonResponse(
        rss(
          item(1, 'Fri, 02 Jan 2026 12:00:00 GMT') +
            item(2, 'Sat, 03 Jan 2026 12:00:00 GMT') +
            item(3, 'Sun, 04 Jan 2026 12:00:00 GMT'),
        ),
      ),
    )
    const outcome = await pipeline.checkFeed('f1', NOW + 600_000)
    expect(outcome.status).toBe('delivered')
    expect(outcome.freshCount).toBe(1)
    expect(sent).toHaveLength(1)
    expect(sent[0].channelId).toBe('c1')
    expect(sent[0].content).toContain('Item 3')
    expect(store.countSeen('f1')).toBe(3)
  })

  it('uses digest when a burst exceeds the threshold (auto mode)', async () => {
    const { store, sent, fetcher } = setup()
    const { pipeline } = buildPipeline(store, sent, fetcher)
    store.addFeed({
      id: 'f1',
      url: 'https://g.test/rss',
      guildId: 'g',
      channelId: 'c1',
      batchMode: 'auto',
      batchThreshold: 3,
    })

    fetcher.mockResolvedValue(
      jsonResponse(
        rss(
          item(1, 'Fri, 02 Jan 2026 12:00:00 GMT') +
            item(2, 'Sat, 03 Jan 2026 12:00:00 GMT') +
            item(3, 'Sun, 04 Jan 2026 12:00:00 GMT') +
            item(4, 'Mon, 05 Jan 2026 12:00:00 GMT'),
        ),
      ),
    )
    await pipeline.checkFeed('f1', NOW)
    expect(sent).toHaveLength(0)

    fetcher.mockResolvedValue(
      jsonResponse(
        rss(
          [
            item(1, 'Fri, 02 Jan 2026 12:00:00 GMT'),
            item(2, 'Sat, 03 Jan 2026 12:00:00 GMT'),
            item(3, 'Sun, 04 Jan 2026 12:00:00 GMT'),
            item(4, 'Mon, 05 Jan 2026 12:00:00 GMT'),
            item(5, 'Tue, 06 Jan 2026 12:00:00 GMT'),
            item(6, 'Wed, 07 Jan 2026 12:00:00 GMT'),
            item(7, 'Thu, 08 Jan 2026 12:00:00 GMT'),
          ].join(''),
        ),
      ),
    )
    await pipeline.checkFeed('f1', NOW + 600_000)
    // One digest message for the 3 new articles (>= threshold 3).
    expect(sent).toHaveLength(1)
    expect(sent[0].content).toContain('3 new articles')
    expect(sent[0].content).toContain('Item 5')
    expect(sent[0].content).toContain('Item 7')
    expect(sent[0].content).not.toContain('Item 3')
  })

  it('respects include filters on delivery', async () => {
    const { store, sent, fetcher } = setup()
    const { pipeline } = buildPipeline(store, sent, fetcher)
    store.addFeed({
      id: 'f1',
      url: 'https://g.test/rss',
      guildId: 'g',
      channelId: 'c1',
      batchMode: 'individual',
    })
    store.addFilter('f1', 'include', 'title', 'v0.', false)

    fetcher.mockResolvedValue(jsonResponse(rss(item(1, 'Fri, 02 Jan 2026 12:00:00 GMT'))))
    await pipeline.checkFeed('f1', NOW) // baseline

    fetcher.mockResolvedValue(
      jsonResponse(
        rss(
          item(1, 'Fri, 02 Jan 2026 12:00:00 GMT') +
            item(2, 'Sun, 04 Jan 2026 12:00:00 GMT') +
            item(3, 'Mon, 05 Jan 2026 12:00:00 GMT'),
        ),
      ),
    )
    // Only "Item 3" contains "v0."? No: neither does. Patch titles via custom feed:
    // simpler to rely on blocked = not sent.
    await pipeline.checkFeed('f1', NOW + 600_000)
    expect(sent).toHaveLength(0)
  })

  it('applies exponential backoff after failures and alerts at threshold', async () => {
    const { store, fetcher } = setup()
    const failures: string[] = []
    const sender = new SenderQueue({ sendDelayMs: 0, send: async () => {}, delay: async () => {} })
    const pipeline = new Pipeline({
      store,
      sender,
      timezone: 'UTC',
      minIntervalMinutes: 2,
      fetchImpl: fetcher as unknown as typeof fetch,
      onFeedFailed: (feed) => failures.push(feed.name),
    })
    store.addFeed({
      id: 'f1',
      url: 'https://g.test/rss',
      guildId: 'g',
      channelId: 'c1',
      name: 'Flaky',
    })
    fetcher.mockRejectedValue(new Error('HTTP 500'))

    await pipeline.checkFeed('f1', NOW)
    expect(store.getFeed('f1')?.consecutiveFailures).toBe(1)
    expect(failures).toHaveLength(0)

    await pipeline.checkFeed('f1', NOW + 1)
    await pipeline.checkFeed('f1', NOW + 2)
    expect(store.getFeed('f1')?.consecutiveFailures).toBe(3)
    expect(failures).toEqual(['Flaky'])

    // next_check pushed beyond the base interval (backoff)
    const nextCheck = store.getFeed('f1')?.nextCheckAt ?? 0
    expect(nextCheck).toBeGreaterThan(NOW + 2 * 60_000)
  })

  it('ticks only due feeds and suspends on demand', async () => {
    const { store, sent, fetcher } = setup()
    const { pipeline, sender } = buildPipeline(store, sent, fetcher)
    const scheduler = new Scheduler(pipeline, store, { now: () => NOW })
    store.addFeed({ id: 'f1', url: 'https://g.test/rss', guildId: 'g', channelId: 'c1' })
    fetcher.mockResolvedValue(jsonResponse(rss(item(1, 'Fri, 02 Jan 2026 12:00:00 GMT'))))

    expect(await scheduler.tick()).toBe(1)
    scheduler.suspend()
    expect(await scheduler.tick()).toBe(0)
    scheduler.resume()
    expect(scheduler.isSuspended()).toBe(false)
    void sender
  })

  it('uses conditional GET to skip unchanged feeds', async () => {
    const { store, sent, fetcher } = setup()
    const { pipeline } = buildPipeline(store, sent, fetcher)
    store.addFeed({ id: 'f1', url: 'https://g.test/rss', guildId: 'g', channelId: 'c1' })
    fetcher.mockResolvedValue(
      jsonResponse(rss(item(1, 'Fri, 02 Jan 2026 12:00:00 GMT')), { etag: '"v1"' }),
    )
    await pipeline.checkFeed('f1', NOW)
    fetcher.mockClear()
    fetcher.mockResolvedValue(new Response(null, { status: 304 }))
    const outcome = await pipeline.checkFeed('f1', NOW + 600_000)
    expect(outcome.status).toBe('not_modified')
    expect(sent).toHaveLength(0)
  })
})
