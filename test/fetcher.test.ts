import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { articleKey, fetchFeed, hashKey, stripHtml, truncate } from '../src/feed/fetcher.js'

const read = (name: string) => readFileSync(`test/fixtures/${name}`, 'utf-8')

function jsonResponse(body: string, headers: Record<string, string> = {}, status = 200): Response {
  return new Response(body, { status, headers })
}

describe('fetchFeed', () => {
  it('parses an Atom feed', async () => {
    const result = await fetchFeed('https://example.test/feed', {
      fetchImpl: (async () => jsonResponse(read('atom.xml'))) as typeof fetch,
    })
    if (result.status !== 'ok') throw new Error('expected ok')
    expect(result.meta.title).toBe('Test Atom Feed')
    expect(result.articles).toHaveLength(2)
    const first = result.articles[0]
    expect(first.title).toBe('First entry')
    expect(first.link).toBe('https://example.com/1')
    expect(first.guid).toBe('urn:test:1')
    expect(first.author).toBe('Alice')
    expect(first.publishedAt?.toISOString()).toBe('2026-01-01T10:00:00.000Z')
    expect(first.image).toBe('https://example.com/img.png')
    expect(first.summary).not.toContain('<b>')
  })

  it('parses an RSS feed with content:encoded', async () => {
    const result = await fetchFeed('https://example.test/feed', {
      fetchImpl: (async () => jsonResponse(read('rss.xml'))) as typeof fetch,
    })
    if (result.status !== 'ok') throw new Error('expected ok')
    expect(result.articles[0].guid).toBe('tag:2026:rel-1')
    expect(result.articles[0].image).toBe('https://example.org/a.png')
    expect(result.articles[0].description).toContain('Full body')
  })

  it('returns not_modified on 304', async () => {
    // Response cannot be constructed with status 304; stub the shape we use.
    const notModified = {
      status: 304,
      ok: false,
      headers: new Headers(),
      arrayBuffer: async () => new ArrayBuffer(0),
    } as unknown as Response
    const result = await fetchFeed('https://example.test/feed', {
      fetchImpl: (async () => notModified) as typeof fetch,
    })
    expect(result.status).toBe('not_modified')
  })

  it('sends conditional headers', async () => {
    let seen: Record<string, string> = {}
    await fetchFeed('https://example.test/feed', {
      etag: '"abc"',
      lastModified: 'Sat, 03 Jan 2026 00:00:00 GMT',
      fetchImpl: (async (_url, init) => {
        seen = Object.fromEntries(new Headers(init?.headers).entries())
        return jsonResponse(read('rss.xml'))
      }) as typeof fetch,
    })
    expect(seen['if-none-match']).toBe('"abc"')
    expect(seen['if-modified-since']).toBe('Sat, 03 Jan 2026 00:00:00 GMT')
  })

  it('throws on HTTP errors', async () => {
    await expect(
      fetchFeed('https://example.test/feed', {
        fetchImpl: (async () => jsonResponse('', {}, 404)) as typeof fetch,
      }),
    ).rejects.toThrow('HTTP 404')
  })
})

describe('articleKey', () => {
  it('prefers guid, then link, then hash', () => {
    expect(articleKey({ guid: 'g1', link: 'l1' })).toBe('g1')
    expect(articleKey({ link: 'l1' })).toBe('l1')
    expect(articleKey({ title: 'T', publishedAt: new Date(0) })).toBe(
      hashKey('T|1970-01-01T00:00:00.000Z'),
    )
  })
})

describe('stripHtml / truncate', () => {
  it('strips HTML and decodes entities', () => {
    expect(stripHtml('<p>The &amp; rest</p>')).toBe('The & rest')
    expect(stripHtml('a<br>b')).toBe('a\nb')
  })
  it('truncates with ellipsis', () => {
    expect(truncate('abcdef', 5)).toHaveLength(5)
    expect(truncate('abc', 5)).toBe('abc')
  })
})
