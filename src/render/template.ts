import { stripHtml, truncate } from '../feed/fetcher.js'
import type { Article } from '../feed/types.js'

export interface TemplateContext {
  article: Article
  feedName: string
  timezone: string
}

export const DEFAULT_TEMPLATE = '**{title}**\n{link}'

/**
 * Render a user template by replacing placeholders.
 * Placeholders: {title} {link} {description} {summary} {author} {date} {image} {feed.name}
 * Unknown placeholders are left untouched.
 */
export function renderTemplate(template: string, context: TemplateContext): string {
  const { article, feedName, timezone } = context
  const description = stripHtml(article.description)
  const link = article.link ?? ''
  const date = article.publishedAt
    ? new Intl.DateTimeFormat('fr-FR', {
        timeZone: timezone,
        dateStyle: 'long',
        timeStyle: 'short',
      }).format(article.publishedAt)
    : ''

  const replacements: Record<string, string> = {
    title: article.title,
    link,
    description,
    summary: truncate(description, 300),
    author: article.author ?? '',
    date,
    image: article.image ?? '',
    'feed.name': feedName,
  }

  return template.replace(/\{([a-z.]+)\}/gi, (match, name: string) => {
    const key = name.toLowerCase()
    const value = replacements[key]
    return value !== undefined ? value : match
  })
}

/** Discord hard limits. */
export const LIMITS = {
  content: 2000,
  embedDescription: 4096,
  embedTitle: 256,
  embedFieldName: 256,
  embedFieldValue: 1024,
  embedFooter: 2048,
  embedAuthor: 256,
  embedFields: 10,
  embedTotal: 6000,
} as const

export function enforceContentLimit(content: string): string {
  if (content.length <= LIMITS.content) return content
  return truncate(content, LIMITS.content)
}
