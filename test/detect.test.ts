import { describe, expect, it } from 'vitest'
import type { FeedFilter } from '../src/db/feeds.js'
import { detectNewArticles, passesFilters } from '../src/feed/detect.js'
import type { Article } from '../src/feed/types.js'

const now = Date.parse('2026-01-10T00:00:00Z')

function article(partial: Partial<Article> & { key: string }): Article {
  return {
    guid: null,
    link: null,
    title: 'Title',
    description: '',
    summary: '',
    author: null,
    publishedAt: new Date(now - 86_400_000),
    image: null,
    ...partial,
  }
}

const noFilters: FeedFilter[] = []

describe('detectNewArticles', () => {
  it('returns unknown articles oldest-first', () => {
    const result = detectNewArticles({
      articles: [
        article({ key: 'new1', publishedAt: new Date(now - 1000) }),
        article({ key: 'old1', publishedAt: new Date(now - 5000) }),
        article({ key: 'seen1' }),
      ],
      knownKeys: new Set(['seen1']),
      maxAgeDays: 30,
      now,
      filters: noFilters,
    })
    expect(result.fresh.map((a) => a.key)).toEqual(['old1', 'new1'])
  })

  it('ignores articles older than maxAgeDays', () => {
    const result = detectNewArticles({
      articles: [article({ key: 'ancient', publishedAt: new Date(now - 90 * 86_400_000) })],
      knownKeys: new Set(),
      maxAgeDays: 30,
      now,
      filters: noFilters,
    })
    expect(result.fresh).toHaveLength(0)
    expect(result.tooOld).toHaveLength(1)
  })

  it('does not check age when maxAgeDays = 0', () => {
    const result = detectNewArticles({
      articles: [article({ key: 'ancient', publishedAt: new Date(now - 90 * 86_400_000) })],
      knownKeys: new Set(),
      maxAgeDays: 0,
      now,
      filters: noFilters,
    })
    expect(result.fresh).toHaveLength(1)
  })

  it('blocks articles failing include filters', () => {
    const filters: FeedFilter[] = [
      { id: 1, feedId: 'f', kind: 'include', field: 'title', pattern: 'v0.', isRegex: false },
    ]
    const result = detectNewArticles({
      articles: [
        article({ key: 'a', title: 'Release v0.1' }),
        article({ key: 'b', title: 'Release v1.0' }),
      ],
      knownKeys: new Set(),
      maxAgeDays: 0,
      now,
      filters,
    })
    expect(result.fresh.map((a) => a.key)).toEqual(['a'])
    expect(result.blocked.map((a) => a.key)).toEqual(['b'])
  })

  it('exclude filters win over includes', () => {
    const filters: FeedFilter[] = [
      { id: 1, feedId: 'f', kind: 'include', field: 'title', pattern: 'release', isRegex: false },
      { id: 2, feedId: 'f', kind: 'exclude', field: 'title', pattern: 'beta', isRegex: false },
    ]
    expect(passesFilters(article({ key: 'a', title: 'release beta' }), filters)).toBe(false)
    expect(passesFilters(article({ key: 'b', title: 'release stable' }), filters)).toBe(true)
  })

  it('supports regex filters', () => {
    const filters: FeedFilter[] = [
      {
        id: 1,
        feedId: 'f',
        kind: 'include',
        field: 'title',
        pattern: '^v\\d+\\.\\d+',
        isRegex: true,
      },
    ]
    expect(passesFilters(article({ key: 'a', title: 'v1.2.3 thing' }), filters)).toBe(true)
    expect(passesFilters(article({ key: 'b', title: 'release v1.2' }), filters)).toBe(false)
  })

  it('matches description when field is any', () => {
    const filters: FeedFilter[] = [
      { id: 1, feedId: 'f', kind: 'include', field: 'any', pattern: 'security', isRegex: false },
    ]
    expect(
      passesFilters(article({ key: 'a', title: 'T', summary: 'a security fix' }), filters),
    ).toBe(true)
    expect(passesFilters(article({ key: 'b', title: 'T', summary: 'nothing here' }), filters)).toBe(
      false,
    )
  })
})
