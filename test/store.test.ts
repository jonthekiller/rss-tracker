import { beforeEach, describe, expect, it } from 'vitest'
import { closeDatabase, type Database, openDatabase } from '../src/db/database.js'
import { FeedStore, type NewFeed } from '../src/db/feeds.js'

describe('FeedStore', () => {
  let db: Database
  let store: FeedStore

  beforeEach(() => {
    db = openDatabase({ dbPath: ':memory:', ensureDir: false })
    store = new FeedStore(db)
  })

  const feed = (partial: Partial<NewFeed> = {}): NewFeed => ({
    id: 'feed-1',
    url: 'https://example.com/feed',
    guildId: 'g1',
    channelId: 'c1',
    ...partial,
  })

  it('adds and retrieves feeds', () => {
    store.addFeed(feed({ name: 'Test' }))
    const retrieved = store.getFeed('feed-1')
    expect(retrieved?.name).toBe('Test')
    expect(retrieved?.templateText).toBe('**{title}**\n{link}')
    expect(retrieved?.batchMode).toBe('auto')
    expect(retrieved?.maxAgeDays).toBe(30)
  })

  it('rejects duplicate url+channel', () => {
    store.addFeed(feed())
    expect(() => store.addFeed(feed({ id: 'feed-2' }))).toThrow()
    // Same URL in another channel is fine.
    expect(() => store.addFeed(feed({ id: 'feed-3', channelId: 'c2' }))).not.toThrow()
  })

  it('updates only provided fields', () => {
    store.addFeed(feed())
    store.updateFeed('feed-1', { name: 'Renamed', intervalMinutes: 42 })
    const updated = store.getFeed('feed-1')
    expect(updated?.name).toBe('Renamed')
    expect(updated?.intervalMinutes).toBe(42)
    expect(updated?.url).toBe('https://example.com/feed')
  })

  it('cascades delete to filters and seen articles', () => {
    store.addFeed(feed())
    store.addFilter('feed-1', 'include', 'title', 'v0.', false)
    store.markSeen('feed-1', ['a', 'b'], [null, null])
    store.deleteFeed('feed-1')
    expect(store.getFeed('feed-1')).toBeNull()
    expect(store.getFilters('feed-1')).toHaveLength(0)
    expect(store.countSeen('feed-1')).toBe(0)
  })

  it('tracks failures and backoff state', () => {
    store.addFeed(feed())
    store.markFailure('feed-1', 1000, 'boom')
    store.markFailure('feed-1', 1001, 'boom again')
    const failing = store.getFeed('feed-1')
    expect(failing?.consecutiveFailures).toBe(2)
    expect(failing?.lastError).toBe('boom again')

    store.markChecked('feed-1', 2000, '"etag1"', null)
    const recovered = store.getFeed('feed-1')
    expect(recovered?.consecutiveFailures).toBe(0)
    expect(recovered?.etag).toBe('"etag1"')
    expect(recovered?.lastError).toBeNull()
  })

  it('returns feeds due for check', () => {
    store.addFeed(feed({ id: 'due' }))
    store.addFeed(feed({ id: 'later', url: 'https://example.com/other', intervalMinutes: 60 }))
    store.setNextCheck('later', Date.now() + 3600_000)
    const due = store.getFeedsDueNow(Date.now(), 10)
    expect(due.map((f) => f.id)).toEqual(['due'])
  })

  it('manages settings', () => {
    store.setSetting('alert_user_id', '123')
    expect(store.getSetting('alert_user_id')).toBe('123')
    store.setSetting('alert_user_id', null)
    expect(store.getSetting('alert_user_id')).toBeNull()
  })

  it('dedupes seen keys', () => {
    store.addFeed(feed())
    store.markSeen('feed-1', ['a', 'b'], [null, null])
    store.markSeen('feed-1', ['b', 'c'], [null, null])
    expect(store.countSeen('feed-1')).toBe(3)
    expect(store.getSeenKeys('feed-1')).toEqual(new Set(['a', 'b', 'c']))
  })

  it('supports regex filters', () => {
    store.addFeed(feed())
    store.addFilter('feed-1', 'exclude', 'description', '^spam\\d+', true)
    const filters = store.getFilters('feed-1')
    expect(filters[0].isRegex).toBe(true)
    store.removeAllFilters('feed-1')
    expect(store.getFilters('feed-1')).toHaveLength(0)
  })

  it('removes a specific filter', () => {
    store.addFeed(feed())
    store.addFilter('feed-1', 'include', 'title', 'a', false)
    store.addFilter('feed-1', 'include', 'title', 'b', false)
    const filters = store.getFilters('feed-1')
    expect(store.removeFilter('feed-1', filters[1].id)).toBe(true)
    expect(store.getFilters('feed-1')).toHaveLength(1)
  })

  it('closes cleanly', () => {
    closeDatabase(db)
  })
})
