import { beforeEach, describe, expect, it } from 'vitest'
import { exportBackup, restoreBackup } from '../src/backup/export.js'
import { openDatabase } from '../src/db/database.js'
import { FeedStore } from '../src/db/feeds.js'
import { importMonitoRssBackup } from '../src/import/monitorss.js'
import { DEFAULT_TEMPLATE } from '../src/render/template.js'

const NOW = Date.parse('2026-01-10T00:00:00Z')

function rss(items: string): string {
  return `<?xml version="1.0"?><rss version="2.0"><channel><title>Generated</title><link>https://g.test</link><description>d</description>${items}</channel></rss>`
}

function item(id: number): string {
  return `<item><title>Item ${id}</title><link>https://g.test/${id}</link><guid>guid-${id}</guid><pubDate>Fri, 02 Jan 2026 12:00:00 GMT</pubDate><description>Body</description></item>`
}

function jsonResponse(body: string): Response {
  return new Response(body, { status: 200 })
}

/** Offline fetch stub: always returns a feed with 2 articles. */
const stubFetch = (async () => jsonResponse(rss(item(1) + item(2)))) as unknown as typeof fetch

async function baselineOk(
  url: string,
): Promise<{ ok: boolean; feedName?: string | undefined; error?: string | undefined }> {
  const { fetchFeed } = await import('../src/feed/fetcher.js')
  const result = await fetchFeed(url, { fetchImpl: stubFetch })
  if (result.status === 'ok') {
    return { ok: true, feedName: result.meta.title ?? undefined }
  }
  return { ok: true }
}

const allowAll = () => Promise.resolve(true)

describe('backup / restore round-trip (v2, per channel)', () => {
  let store: FeedStore

  beforeEach(() => {
    store = new FeedStore(openDatabase({ dbPath: ':memory:', ensureDir: false }))
  })

  it('exports and restores state identically', async () => {
    store.addFeed({
      id: '11111111-1111-1111-1111-111111111111',
      name: 'My Feed',
      url: 'https://example.com/rss',
      guildId: 'g1',
      channelId: 'c1',
      templateText: ':newspaper: **{title}**\n{link}',
      intervalMinutes: 15,
      batchMode: 'digest',
      batchThreshold: 4,
    })
    store.addFilter('11111111-1111-1111-1111-111111111111', 'include', 'title', 'v0.', false)
    store.markSeen('11111111-1111-1111-1111-111111111111', ['k1', 'k2'], [NOW, NOW])
    store.setChannelSetting('c1', 'alert_user_id', '999')

    const backup = exportBackup(store)
    expect(backup.version).toBe(2)
    expect(backup.channels).toHaveLength(1)
    expect(backup.channels[0].channelId).toBe('c1')
    expect(backup.channels[0].feeds).toHaveLength(1)
    expect(backup.channels[0].feeds[0].seenKeys).toEqual(['k1', 'k2'])
    expect(backup.channels[0].feeds[0].filters).toHaveLength(1)
    expect(backup.channels[0].settings.alert_user_id).toBe('999')

    // Fresh database (wipe): restore from the backup.
    const store2 = new FeedStore(openDatabase({ dbPath: ':memory:', ensureDir: false }))
    const results = await restoreBackup(store2, backup, {
      guildId: 'g1',
      channelExists: () => Promise.resolve(true),
      actorCanManageChannel: allowAll,
      baseline: baselineOk,
    })
    expect(results.every((r) => r.status === 'restored')).toBe(true)

    const restored = store2.getFeed('11111111-1111-1111-1111-111111111111')
    expect(restored?.name).toBe('My Feed')
    expect(restored?.templateText).toBe(':newspaper: **{title}**\n{link}')
    expect(restored?.intervalMinutes).toBe(15)
    expect(restored?.batchMode).toBe('digest')
    expect(store2.getSeenKeys('11111111-1111-1111-1111-111111111111')).toEqual(
      new Set(['k1', 'k2']),
    )
    expect(store2.getChannelSetting('c1', 'alert_user_id')).toBe('999')
    expect(store2.getFilters('11111111-1111-1111-1111-111111111111')[0].pattern).toBe('v0.')
  })

  it('exports a single channel when a scope is given', async () => {
    store.addFeed({ id: 'feed-a', name: 'A', url: 'https://a', guildId: 'g1', channelId: 'c1' })
    store.addFeed({ id: 'feed-b', name: 'B', url: 'https://b', guildId: 'g1', channelId: 'c2' })
    const backup = exportBackup(store, ['c1'])
    expect(backup.channels).toHaveLength(1)
    expect(backup.channels[0].channelId).toBe('c1')
    expect(backup.channels[0].feeds).toHaveLength(1)
  })

  it('puts feeds with unknown channels on pause', async () => {
    store.addFeed({
      id: 'feed-a',
      name: 'A',
      url: 'https://a',
      guildId: 'g1',
      channelId: 'old-channel',
    })
    const backup = exportBackup(store)
    const store2 = new FeedStore(openDatabase({ dbPath: ':memory:', ensureDir: false }))
    const results = await restoreBackup(store2, backup, {
      guildId: 'g1',
      channelExists: () => Promise.resolve(false),
      actorCanManageChannel: allowAll,
      baseline: baselineOk,
    })
    expect(results[0].status).toBe('channel_missing')
  })

  it('skips channel groups the actor cannot manage', async () => {
    store.addFeed({ id: 'feed-a', name: 'A', url: 'https://a', guildId: 'g1', channelId: 'c1' })
    const backup = exportBackup(store)
    const store2 = new FeedStore(openDatabase({ dbPath: ':memory:', ensureDir: false }))
    const results = await restoreBackup(store2, backup, {
      guildId: 'g1',
      channelExists: () => Promise.resolve(true),
      actorCanManageChannel: () => Promise.resolve(false),
      baseline: baselineOk,
    })
    expect(results[0].status).toBe('forbidden')
    expect(store2.getFeed('feed-a')).toBeNull()
  })

  it('remaps channels on restore', async () => {
    store.addFeed({ id: 'feed-a', name: 'A', url: 'https://a', guildId: 'g1', channelId: 'old' })
    const backup = exportBackup(store)
    const store2 = new FeedStore(openDatabase({ dbPath: ':memory:', ensureDir: false }))
    await restoreBackup(store2, backup, {
      guildId: 'g1',
      mapChannels: { old: 'new' },
      channelExists: () => Promise.resolve(false),
      actorCanManageChannel: allowAll,
      baseline: baselineOk,
    })
    expect(store2.getFeed('feed-a')?.channelId).toBe('new')
  })

  it('rejects invalid backups', async () => {
    const store2 = new FeedStore(openDatabase({ dbPath: ':memory:', ensureDir: false }))
    await expect(
      restoreBackup(
        store2,
        { version: 99, feeds: [] },
        {
          guildId: 'g1',
          channelExists: () => Promise.resolve(true),
          actorCanManageChannel: allowAll,
          baseline: baselineOk,
        },
      ),
    ).rejects.toThrow('Unsupported backup version')
  })

  it('normalizes legacy v1 backups (flat feeds)', async () => {
    const v1 = {
      version: 1,
      exportedAt: '2026-01-01T00:00:00Z',
      settings: { alert_user_id: 'ignored-in-v2' },
      feeds: [
        {
          id: 'feed-x',
          name: 'X',
          url: 'https://x',
          guildId: 'g1',
          channelId: 'c1',
          enabled: true,
          intervalMinutes: 10,
          templateText: '{title}',
          embedJson: null,
          batchMode: 'auto',
          batchThreshold: 5,
          batchMaxItems: 15,
          maxAgeDays: 30,
          pauseReason: null,
          filters: [],
          seenKeys: [],
        },
      ],
    }
    const store2 = new FeedStore(openDatabase({ dbPath: ':memory:', ensureDir: false }))
    const results = await restoreBackup(store2, v1, {
      guildId: 'g1',
      channelExists: () => Promise.resolve(true),
      actorCanManageChannel: allowAll,
      baseline: baselineOk,
    })
    expect(results[0].status).toBe('restored')
    expect(store2.getFeed('feed-x')?.channelId).toBe('c1')
    // v1 global settings are intentionally dropped (per-channel model now).
    expect(store2.getChannelSetting('c1', 'alert_user_id')).toBeNull()
  })
})

describe('MonitoRSS import (channel-scoped)', () => {
  let store: FeedStore

  beforeEach(() => {
    store = new FeedStore(openDatabase({ dbPath: ':memory:', ensureDir: false }))
  })

  const monitoBackup = {
    profile: { name: 'Jonthekiller server', alert: ['189283824008429568'] },
    feeds: [
      {
        title: 'Google Workspace',
        url: 'http://workspaceupdates.googleblog.com/atom.xml',
        guild: '1100111551412850741',
        channel: '1100112370086465660',
        text: ':newspaper:  |  **{title}**\n\n{link}',
        embeds: [],
      },
      {
        title: 'Release notes from hermes-webui',
        url: 'https://github.com/nesquena/hermes-webui/releases.atom',
        guild: '1100111551412850741',
        channel: '1100112370086465660',
        filters: { title: ['v0.'] },
        ncomparisons: ['title'],
      },
      { title: 'Broken', url: '', channel: 'x' },
    ],
    filteredFormats: [],
    subscribers: [],
  }

  it('imports feeds with templates and filters, baseline without sending', async () => {
    const results = await importMonitoRssBackup(store, monitoBackup, {
      guildId: 'g1',
      fallbackChannelId: 'fallback-channel',
      channelExists: (_g, channelId) => Promise.resolve(channelId === '1100112370086465660'),
      actorCanManageChannel: allowAll,
      defaultTemplate: DEFAULT_TEMPLATE,
      fetchImpl: stubFetch,
    })

    expect(results).toHaveLength(3)
    expect(results.filter((r) => r.status === 'imported')).toHaveLength(2)
    expect(results.find((r) => r.name === 'Broken')?.status).toBe('skipped')

    const feeds = store.getAllFeeds()
    expect(feeds).toHaveLength(2)
    const google = feeds.find((f) => f.name === 'Google Workspace')
    expect(google?.templateText).toBe(':newspaper:  |  **{title}**\n\n{link}')
    expect(google?.channelId).toBe('1100112370086465660')
    // Baseline was fetched with the offline stub → 2 articles seen, nothing sent.
    expect(store.countSeen(google?.id ?? '')).toBe(2)

    const hermes = feeds.find((f) => f.name === 'Release notes from hermes-webui')
    expect(store.getFilters(hermes?.id ?? '')).toHaveLength(1)
    expect(store.getFilters(hermes?.id ?? '')[0].pattern).toBe('v0.')
  })

  it('uses the fallback channel when the backup channel is missing', async () => {
    const results = await importMonitoRssBackup(store, monitoBackup, {
      guildId: 'g1',
      fallbackChannelId: 'fallback',
      channelExists: () => Promise.resolve(false),
      actorCanManageChannel: allowAll,
      defaultTemplate: DEFAULT_TEMPLATE,
      fetchImpl: stubFetch,
    })
    expect(results.every((r) => r.status === 'imported' || r.status === 'skipped')).toBe(true)
    const feeds = store.getAllFeeds()
    expect(feeds.every((f) => f.channelId === 'fallback')).toBe(true)
  })

  it('skips feeds the actor cannot manage', async () => {
    const results = await importMonitoRssBackup(store, monitoBackup, {
      guildId: 'g1',
      fallbackChannelId: 'fallback',
      channelExists: () => Promise.resolve(false),
      actorCanManageChannel: () => Promise.resolve(false),
      defaultTemplate: DEFAULT_TEMPLATE,
      fetchImpl: stubFetch,
    })
    expect(
      results.filter((r) => r.status === 'skipped' && r.detail?.includes('Send Messages')),
    ).toHaveLength(2)
    expect(store.getAllFeeds()).toHaveLength(0)
  })

  it('skips feeds already imported for the same channel', async () => {
    const options = {
      guildId: 'g1',
      fallbackChannelId: 'fallback',
      channelExists: () => Promise.resolve(false),
      actorCanManageChannel: allowAll,
      defaultTemplate: DEFAULT_TEMPLATE,
      fetchImpl: stubFetch,
    }
    await importMonitoRssBackup(store, monitoBackup, options)
    const results = await importMonitoRssBackup(store, monitoBackup, options)
    expect(
      results.filter((r) => r.status === 'skipped' && r.detail?.includes('already exists')),
    ).toHaveLength(2)
  })

  it('maps profile.alert to the target channel settings', async () => {
    await importMonitoRssBackup(store, monitoBackup, {
      guildId: 'g1',
      fallbackChannelId: 'fallback',
      channelExists: () => Promise.resolve(false),
      actorCanManageChannel: allowAll,
      defaultTemplate: DEFAULT_TEMPLATE,
      fetchImpl: stubFetch,
    })
    expect(store.getChannelSetting('fallback', 'alert_user_id')).toBe('189283824008429568')
  })

  it('rejects non-MonitoRSS JSON', async () => {
    await expect(
      importMonitoRssBackup(
        store,
        { foo: 1 },
        {
          guildId: 'g1',
          fallbackChannelId: 'c',
          channelExists: () => Promise.resolve(true),
          actorCanManageChannel: allowAll,
          defaultTemplate: DEFAULT_TEMPLATE,
        },
      ),
    ).rejects.toThrow('feeds')
  })

  it('imports the full real backup (22 feeds)', async () => {
    const { readFileSync } = await import('node:fs')
    const real = JSON.parse(readFileSync('test/fixtures/monitorss-backup.json', 'utf-8')) as object
    const results = await importMonitoRssBackup(store, real, {
      guildId: 'g1',
      fallbackChannelId: 'fallback',
      channelExists: () => Promise.resolve(true),
      actorCanManageChannel: allowAll,
      defaultTemplate: DEFAULT_TEMPLATE,
      fetchImpl: stubFetch,
    })
    expect(results.filter((r) => r.status === 'imported')).toHaveLength(22)
    expect(store.getAllFeeds()).toHaveLength(22)
    // The v0. filter on hermes-webui is preserved.
    const hermes = store.getAllFeeds().find((f) => f.name.includes('hermes-webui'))
    expect(hermes).toBeDefined()
    expect(store.getFilters(hermes?.id ?? '')[0]?.pattern).toBe('v0.')
  })
})
