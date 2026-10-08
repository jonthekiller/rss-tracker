import type { FeedFilter } from '../db/feeds.js'
import type { Article } from './types.js'

export interface DetectionInput {
  /** All articles currently in the feed XML, oldest-first order preserved from the feed. */
  articles: Article[]
  /** Keys already known (persisted + current cycle cache). */
  knownKeys: Set<string>
  /** 0 = disabled. Articles older than this are ignored (guid-regeneration guard). */
  maxAgeDays: number
  now: number
  filters: FeedFilter[]
}

export interface DetectionResult {
  /** New articles that passed filters and age checks, oldest first. */
  fresh: Article[]
  /** Articles blocked by include/exclude filters. */
  blocked: Article[]
  /** Articles ignored because they are too old. */
  tooOld: Article[]
}

/**
 * Determine which articles are new. Articles are returned oldest-first so that
 * delivery order matches publication order.
 */
export function detectNewArticles(input: DetectionInput): DetectionResult {
  const maxAgeMs = input.maxAgeDays > 0 ? input.maxAgeDays * 86_400_000 : 0
  const fresh: Article[] = []
  const blocked: Article[] = []
  const tooOld: Article[] = []

  for (const article of input.articles) {
    if (input.knownKeys.has(article.key)) continue
    if (
      maxAgeMs > 0 &&
      article.publishedAt &&
      input.now - article.publishedAt.getTime() > maxAgeMs
    ) {
      tooOld.push(article)
      continue
    }
    if (!passesFilters(article, input.filters)) {
      blocked.push(article)
      continue
    }
    fresh.push(article)
  }

  // Oldest first so messages are posted in publication order.
  fresh.sort((a, b) => (a.publishedAt?.getTime() ?? 0) - (b.publishedAt?.getTime() ?? 0))
  return { fresh, blocked, tooOld }
}

export function passesFilters(article: Article, filters: FeedFilter[]): boolean {
  const includes = filters.filter((f) => f.kind === 'include')
  const excludes = filters.filter((f) => f.kind === 'exclude')

  if (excludes.some((f) => matchFilter(article, f))) return false

  if (includes.length > 0) {
    // Articles must match at least one include filter to be sent.
    return includes.some((f) => matchFilter(article, f))
  }
  return true
}

function matchFilter(article: Article, filter: FeedFilter): boolean {
  const fields: string[] = []
  if (filter.field === 'title' || filter.field === 'any') fields.push(article.title)
  if (filter.field === 'description' || filter.field === 'any')
    fields.push(article.summary, article.description)

  if (filter.isRegex) {
    let regex: RegExp
    try {
      regex = new RegExp(filter.pattern, 'i')
    } catch {
      return false
    }
    return fields.some((field) => regex.test(field))
  }
  const needle = filter.pattern.toLowerCase()
  return fields.some((field) => field.toLowerCase().includes(needle))
}
