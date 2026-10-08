import type { APIEmbed } from 'discord.js'
import {
  DEFAULT_TEMPLATE,
  enforceContentLimit,
  LIMITS,
  renderTemplate,
  type TemplateContext,
} from './template.js'

/**
 * User-facing embed template (JSON stored per feed).
 * Placeholders are the same as text templates. `timestamp` may be
 * "article" (article date), "now" (send time) or null/absent.
 */
export interface EmbedTemplate {
  title?: string
  description?: string
  url?: string
  color?: number | string
  footerText?: string
  footerIconURL?: string
  authorName?: string
  authorURL?: string
  authorIconURL?: string
  thumbnailURL?: string
  imageURL?: string
  timestamp?: 'article' | 'now' | null
  fields?: { name: string; value: string; inline?: boolean }[]
}

export function parseEmbedTemplate(json: string | null): EmbedTemplate | null {
  if (!json) return null
  try {
    const parsed = JSON.parse(json) as unknown
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    return parsed as EmbedTemplate
  } catch {
    return null
  }
}

/** Render an embed template into a Discord APIEmbed, enforcing all limits. Returns null when it does not fit. */
export function renderEmbed(template: EmbedTemplate, context: TemplateContext): APIEmbed {
  const embed: APIEmbed = {}

  const title = template.title ? renderTemplate(template.title, context) : undefined
  if (title) embed.title = title.slice(0, LIMITS.embedTitle)

  const description = template.description
    ? renderTemplate(template.description, context)
    : undefined
  if (description) embed.description = description.slice(0, LIMITS.embedDescription)

  const url = template.url ? renderTemplate(template.url, context) : undefined
  if (url && isSafeUrl(url)) embed.url = url

  if (template.color !== undefined) {
    const color = normalizeColor(template.color)
    if (color !== null) embed.color = color
  }

  const footerText = template.footerText ? renderTemplate(template.footerText, context) : undefined
  if (footerText) {
    embed.footer = { text: footerText.slice(0, LIMITS.embedFooter) }
    const footerIcon = template.footerIconURL
      ? renderTemplate(template.footerIconURL, context)
      : undefined
    if (footerIcon && isSafeUrl(footerIcon)) embed.footer.icon_url = footerIcon
  }

  const authorName = template.authorName ? renderTemplate(template.authorName, context) : undefined
  if (authorName) {
    embed.author = { name: authorName.slice(0, LIMITS.embedAuthor) }
    const authorUrl = template.authorURL ? renderTemplate(template.authorURL, context) : undefined
    if (authorUrl && isSafeUrl(authorUrl)) embed.author.url = authorUrl
    const authorIcon = template.authorIconURL
      ? renderTemplate(template.authorIconURL, context)
      : undefined
    if (authorIcon && isSafeUrl(authorIcon)) embed.author.icon_url = authorIcon
  }

  const thumbnail = template.thumbnailURL
    ? renderTemplate(template.thumbnailURL, context)
    : undefined
  if (thumbnail && isSafeUrl(thumbnail)) embed.thumbnail = { url: thumbnail }

  const image = template.imageURL ? renderTemplate(template.imageURL, context) : undefined
  if (image && isSafeUrl(image)) embed.image = { url: image }

  if (template.timestamp === 'article' && context.article.publishedAt) {
    embed.timestamp = context.article.publishedAt.toISOString()
  } else if (template.timestamp === 'now') {
    embed.timestamp = new Date().toISOString()
  }

  if (Array.isArray(template.fields) && template.fields.length > 0) {
    const fields = template.fields.slice(0, LIMITS.embedFields).map((field) => {
      const name =
        renderTemplate(field.name ?? '', context).slice(0, LIMITS.embedFieldName) || '\u200b'
      const value =
        renderTemplate(field.value ?? '', context).slice(0, LIMITS.embedFieldValue) || '\u200b'
      return { name, value, inline: field.inline === true }
    })
    embed.fields = fields
  }

  return embed
}

function normalizeColor(color: number | string): number | null {
  if (typeof color === 'number') {
    return color >= 0 && color <= 0xffffff ? color : null
  }
  const hex = color.replace('#', '')
  if (!/^[0-9a-fA-F]{6}$/.test(hex)) return null
  return Number.parseInt(hex, 16)
}

/** Reject non-http(s) URLs (javascript: etc.) to avoid embed errors. */
export function isSafeUrl(url: string): boolean {
  try {
    const parsed = new URL(url)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

/** Build the outgoing payload for an article: content + optional embeds. */
export function buildArticlePayload(
  templateText: string,
  embedJson: string | null,
  context: TemplateContext,
): { content: string; embeds: APIEmbed[] } {
  let content = renderTemplate(
    templateText === '{empty}' ? DEFAULT_TEMPLATE : templateText,
    context,
  )
  content = enforceContentLimit(content)

  const embeds: APIEmbed[] = []
  const embedTemplate = parseEmbedTemplate(embedJson)
  if (embedTemplate) {
    try {
      embeds.push(renderEmbed(embedTemplate, context))
    } catch {
      // Invalid embed template: fall back to text-only (never lose the article).
    }
  }

  if (content.length === 0 && embeds.length === 0) {
    content = `Unable to render article: ${context.article.link ?? context.article.key}`
  }

  return { content, embeds }
}
