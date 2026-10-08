export interface Article {
  /** Stable unique key within a feed: guid || link || hash(title+pubdate) */
  key: string
  guid: string | null
  link: string | null
  title: string
  /** Raw HTML content, if any */
  description: string
  /** Plain-text truncated summary (no HTML), max 300 chars */
  summary: string
  author: string | null
  publishedAt: Date | null
  image: string | null
}

export interface FeedMeta {
  title: string | null
}

export type FetchFeedResult =
  | { status: 'not_modified' }
  | {
      status: 'ok'
      articles: Article[]
      meta: FeedMeta
      etag: string | null
      lastModified: string | null
    }

export class FeedFetchError extends Error {
  constructor(
    message: string,
    readonly statusCode?: number,
  ) {
    super(message)
    this.name = 'FeedFetchError'
  }
}
