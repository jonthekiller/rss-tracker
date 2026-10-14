import { describe, expect, it } from 'vitest'
import type { Article } from '../src/feed/types.js'
import { buildDigestMessages } from '../src/render/digest.js'
import { buildArticlePayload, type EmbedTemplate, renderEmbed } from '../src/render/embed.js'
import { DEFAULT_TEMPLATE, enforceContentLimit, renderTemplate } from '../src/render/template.js'

const article: Article = {
  key: 'k1',
  guid: 'g1',
  link: 'https://example.com/1',
  title: 'A very long title about things',
  description: '<p>Hello <b>world</b> &amp; friends</p>',
  summary: 'Hello world & friends',
  author: 'Alice',
  publishedAt: new Date('2026-01-03T12:00:00Z'),
  image: 'https://example.com/img.png',
}

const context = { article, feedName: 'My Feed', timezone: 'UTC' }

describe('renderTemplate', () => {
  it('replaces all placeholders', () => {
    const out = renderTemplate('{title}\n{link}\n{summary}\n{author}\n{feed.name}', context)
    expect(out).toContain('A very long title about things')
    expect(out).toContain('https://example.com/1')
    expect(out).toContain('Hello world & friends')
    expect(out).toContain('Alice')
    expect(out).toContain('My Feed')
  })

  it('strips HTML from {description}', () => {
    expect(renderTemplate('{description}', context)).toBe('Hello world & friends')
  })

  it('leaves unknown placeholders untouched', () => {
    expect(renderTemplate('{nope} {title}', context)).toBe('{nope} A very long title about things')
  })

  it('keeps MonitoRSS-style templates working', () => {
    const template = ':newspaper:  |  **{title}**\n\n{link}\n\n{description}'
    const out = renderTemplate(template, context)
    expect(out).toMatch(/^:newspaper: {2}\| {2}\*\*A very long title/)
    expect(out).toContain('Hello world & friends')
  })
})

describe('enforceContentLimit', () => {
  it('truncates content beyond 2000 chars', () => {
    const long = 'x'.repeat(3000)
    expect(enforceContentLimit(long).length).toBeLessThanOrEqual(2000)
    expect(enforceContentLimit(long).endsWith('…')).toBe(true)
  })
})

describe('buildArticlePayload', () => {
  it('returns text-only by default', () => {
    const payload = buildArticlePayload(DEFAULT_TEMPLATE, null, context)
    expect(payload.content).toBe('**A very long title about things**\nhttps://example.com/1')
    expect(payload.embeds).toHaveLength(0)
  })

  it('falls back to text when embed JSON is invalid', () => {
    const payload = buildArticlePayload(DEFAULT_TEMPLATE, '{not json', context)
    expect(payload.embeds).toHaveLength(0)
    expect(payload.content).toContain('A very long title')
  })

  it('renders a valid embed template', () => {
    const embed: EmbedTemplate = {
      title: '{title}',
      description: '{description}',
      color: '#ff0000',
      timestamp: 'article',
      fields: [{ name: 'Author', value: '{author}' }],
    }
    const payload = buildArticlePayload('{empty}', JSON.stringify(embed), context)
    expect(payload.embeds).toHaveLength(1)
    expect(payload.embeds[0].title).toBe('A very long title about things')
    expect(payload.embeds[0].color).toBe(0xff0000)
    expect(payload.embeds[0].timestamp).toBe('2026-01-03T12:00:00.000Z')
    expect(payload.embeds[0].fields?.[0].value).toBe('Alice')
  })
})

describe('renderEmbed limits', () => {
  it('clamps title and description to Discord limits', () => {
    const rendered = renderEmbed({ title: 't'.repeat(500), description: 'd'.repeat(5000) }, context)
    expect(rendered.title?.length).toBeLessThanOrEqual(256)
    expect(rendered.description?.length).toBeLessThanOrEqual(4096)
  })

  it('caps fields at 10', () => {
    const rendered = renderEmbed(
      { fields: Array.from({ length: 15 }, (_, i) => ({ name: `n${i}`, value: 'v' })) },
      context,
    )
    expect(rendered.fields).toHaveLength(10)
  })

  it('rejects non-http URLs', () => {
    const rendered = renderEmbed({ url: 'javascript:alert(1)' }, context)
    expect(rendered.url).toBeUndefined()
  })
})

describe('buildDigestMessages', () => {
  const items = Array.from({ length: 8 }, (_, i) => ({
    title: `Article ${i + 1}`,
    link: `https://x/${i}`,
  }))

  it('lists every article in a single message when it fits', () => {
    const [message] = buildDigestMessages(items, {
      feedName: 'F',
      timezone: 'UTC',
    })
    expect(message.content).toContain('8 new articles')
    expect(message.content).toContain('1. [Article 1](<https://x/0>)')
    expect(message.content).toContain('8. [Article 8](<https://x/7>)')
  })

  it('splits long digests under 2000 chars, listing every article', () => {
    const many = Array.from({ length: 200 }, (_, i) => ({
      title: `Article number ${i} with a fairly long title to fill space quickly`,
      link: `https://example.com/articles/${i}`,
    }))
    const messages = buildDigestMessages(many, {
      feedName: 'F',
      timezone: 'UTC',
    })
    expect(messages.length).toBeGreaterThan(1)
    for (const message of messages) {
      expect(message.content.length).toBeLessThanOrEqual(2000)
    }
    expect(messages[0].content).toContain('200 new articles')
    expect(messages[0].content).not.toContain('…and')
    expect(messages[1].content).not.toContain('new articles')
    // Every article appears exactly once across the messages.
    const all = messages.map((m) => m.content).join('\n')
    for (let i = 0; i < 200; i += 1) {
      expect(all).toContain(`Article number ${i} `)
    }
  })

  it('handles articles without links', () => {
    const [message] = buildDigestMessages([{ title: 'No link', link: null }], {
      feedName: 'F',
      timezone: 'UTC',
    })
    expect(message.content).toContain('1. No link')
  })
})
