import { createHash } from 'node:crypto'
import { parseFeed } from 'feedsmith'
import type { Article, FeedMeta, FetchFeedResult } from './types.js'
import { FeedFetchError } from './types.js'

const USER_AGENT = 'RRS-Tracker (+https://github.com/jonthekiller/rss-tracker)'
const TIMEOUT_MS = 15_000
const MAX_BYTES = 10 * 1024 * 1024

export interface FetchOptions {
  etag?: string | null
  lastModified?: string | null
  timeoutMs?: number
  fetchImpl?: typeof fetch | undefined
}

/** SHA-1 hex of a string. */
export function hashKey(input: string): string {
  return createHash('sha1').update(input).digest('hex')
}

/**
 * Compute the stable article key: guid || link || hash(title+pubdate).
 */
export function articleKey(input: {
  guid?: string | null
  link?: string | null
  title?: string | null
  publishedAt?: Date | null
}): string {
  if (input.guid) return input.guid
  if (input.link) return input.link
  return hashKey(`${input.title ?? ''}|${input.publishedAt?.toISOString() ?? ''}`)
}

export function stripHtml(html: string): string {
  let text = html
    .replace(/<style[\s\S]*?<\/style>/gi, ' ')
    .replace(/<script[\s\S]*?<\/script>/gi, ' ')
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<\/(p|div|li|h[1-6])>/gi, '\n')
    .replace(/<[^>]+>/g, ' ')
  text = decodeEntities(text)
  return text
    .replace(/[ \t]+/g, ' ')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

export function decodeEntities(text: string): string {
  return text
    .replace(/&nbsp;/g, ' ')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#0?39;/g, "'")
    .replace(/&apos;/g, "'")
    .replace(/&hellip;/g, '…')
    .replace(/&mdash;/g, '—')
    .replace(/&ndash;/g, '–')
    .replace(/&#(\d+);/g, (_, code: string) => String.fromCodePoint(Number(code)))
    .replace(/&#x([0-9a-fA-F]+);/g, (_, code: string) =>
      String.fromCodePoint(Number.parseInt(code, 16)),
    )
}

export function truncate(text: string, maxLength: number): string {
  if (text.length <= maxLength) return text
  return `${text.slice(0, Math.max(0, maxLength - 1)).trimEnd()}…`
}

export function extractFirstImage(html: string): string | null {
  const match = /<img[^>]+src=["']([^"']+)["']/i.exec(html)
  return match?.[1] ?? null
}

// The feedsmith parsed union is heavily nested; a loose record keeps the
// normalizer readable while staying contained in this file.
// biome-ignore lint/suspicious/noExplicitAny: intentional loose parsing of third-party union types
type LooseRecord = Record<string, any>

function normalizeEntries(parsed: ReturnType<typeof parseFeed>): {
  articles: Article[]
  meta: FeedMeta
} {
  const articles: Article[] = []
  let meta: FeedMeta = { title: null }

  const push = (input: {
    guid?: string | null
    link?: string | null
    title?: string | null
    description?: string | null
    author?: string | null
    publishedAt?: Date | null
    image?: string | null
  }) => {
    const title = (input.title ?? '').trim()
    const description = input.description ?? ''
    articles.push({
      key: articleKey(input),
      guid: input.guid ?? null,
      link: input.link ?? null,
      title,
      description,
      summary: truncate(stripHtml(description), 300),
      author: input.author?.trim() || null,
      publishedAt: input.publishedAt ?? null,
      image: input.image ?? null,
    })
  }

  if (parsed.format === 'atom') {
    const feed = parsed.feed as LooseRecord
    meta = { title: textValue(feed.title) }
    for (const entry of (feed.entries ?? []) as LooseRecord[]) {
      const links = (entry.links ?? []) as LooseRecord[]
      const alternate = links.find((l) => !l.rel || l.rel === 'alternate')
      const content = textValue(entry.content)
      const description = content || textValue(entry.summary) || ''
      push({
        guid: typeof entry.id === 'string' ? entry.id : null,
        link: typeof alternate?.href === 'string' ? alternate.href : null,
        title: textValue(entry.title),
        description,
        author: firstAuthor(entry.authors),
        publishedAt: toDate(entry.published) ?? toDate(entry.updated) ?? null,
        image: content ? extractFirstImage(content) : null,
      })
    }
  } else if (parsed.format === 'rss' || parsed.format === 'rdf') {
    const feed = parsed.feed as LooseRecord
    meta = { title: typeof feed.title === 'string' ? feed.title : null }
    const items = (feed.items ?? []) as LooseRecord[]
    for (const item of items) {
      const contentEncoded = item.content?.encoded
      const description = contentEncoded ?? item.description ?? ''
      const enclosures = (item.enclosures ?? []) as LooseRecord[]
      const enclosureImage = enclosures.find(
        (e) => typeof e.url === 'string' && String(e.type ?? '').startsWith('image/'),
      )
      push({
        guid: item.guid?.value ?? null,
        link: typeof item.link === 'string' ? item.link : null,
        title: typeof item.title === 'string' ? item.title : null,
        description,
        author: firstAuthor(item.authors) ?? (typeof item.author === 'string' ? item.author : null),
        publishedAt: toDate(item.pubDate) ?? null,
        image:
          (typeof enclosureImage?.url === 'string' ? enclosureImage.url : null) ??
          extractFirstImage(description),
      })
    }
  } else {
    const feed = parsed.feed as LooseRecord
    meta = { title: typeof feed.title === 'string' ? feed.title : null }
    for (const item of (feed.items ?? []) as LooseRecord[]) {
      const description = item.content_html ?? item.content_text ?? ''
      push({
        guid: typeof item.id === 'string' ? item.id : null,
        link: typeof item.url === 'string' ? item.url : null,
        title: typeof item.title === 'string' ? item.title : null,
        description,
        author: firstAuthor(item.authors),
        publishedAt: toDate(item.date_published) ?? toDate(item.date_modified) ?? null,
        image:
          (typeof item.image === 'string' ? item.image : null) ?? extractFirstImage(description),
      })
    }
  }

  return { articles, meta }
}

function textValue(value: unknown): string | null {
  if (typeof value === 'string') return value
  if (value && typeof value === 'object' && typeof (value as LooseRecord).value === 'string') {
    return (value as LooseRecord).value as string
  }
  return null
}

function firstAuthor(authors: unknown): string | null {
  if (!Array.isArray(authors) || authors.length === 0) return null
  const first = authors[0] as LooseRecord
  return typeof first?.name === 'string' ? first.name : null
}

function toDate(value: unknown): Date | null {
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value
  if (typeof value !== 'string') return null
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

/**
 * Fetch a feed with conditional GET support. Returns 'not_modified' on HTTP 304
 * or throws FeedFetchError on failure.
 */
export async function fetchFeed(url: string, options: FetchOptions = {}): Promise<FetchFeedResult> {
  const fetchImpl = options.fetchImpl ?? fetch
  const headers: Record<string, string> = {
    'user-agent': USER_AGENT,
    accept: 'application/rss+xml, application/atom+xml, application/xml, text/xml, */*',
  }
  if (options.etag) headers['if-none-match'] = options.etag
  if (options.lastModified) headers['if-modified-since'] = options.lastModified

  let response: Response
  try {
    response = await fetchImpl(url, {
      headers,
      redirect: 'follow',
      signal: AbortSignal.timeout(options.timeoutMs ?? TIMEOUT_MS),
    })
  } catch (error) {
    throw new FeedFetchError(`Failed to reach feed: ${(error as Error).message}`)
  }

  if (response.status === 304) {
    return { status: 'not_modified' }
  }
  if (!response.ok) {
    throw new FeedFetchError(`HTTP ${response.status} ${response.statusText}`, response.status)
  }

  const buffer = await response.arrayBuffer()
  if (buffer.byteLength > MAX_BYTES) {
    throw new FeedFetchError(`Feed too large (${buffer.byteLength} bytes)`)
  }
  const xml = new TextDecoder('utf-8', { fatal: false }).decode(buffer)

  let parsed: ReturnType<typeof parseFeed>
  try {
    parsed = parseFeed(xml)
  } catch (error) {
    throw new FeedFetchError(`Malformed feed: ${(error as Error).message}`)
  }

  const { articles, meta } = normalizeEntries(parsed)
  return {
    status: 'ok',
    articles,
    meta,
    etag: response.headers.get('etag'),
    lastModified: response.headers.get('last-modified'),
  }
}
