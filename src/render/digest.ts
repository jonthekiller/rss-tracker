import type { APIEmbed } from 'discord.js'
import { LIMITS } from './template.js'

export interface DigestItem {
  title: string
  link: string | null
}

export interface DigestOptions {
  feedName: string
  timezone: string
}

export interface DigestMessage {
  content: string
  embeds: APIEmbed[]
}

/**
 * Build a digest message for a burst of articles. Every item is listed; when
 * the recap does not fit in a single message it is split into as many
 * messages as needed (max 2000 chars each), header only on the first one.
 */
export function buildDigestMessages(items: DigestItem[], options: DigestOptions): DigestMessage[] {
  if (items.length === 0) return []

  const lines: string[] = items.map((item, index) => {
    const number = `${index + 1}.`
    const title = item.title.replace(/\n+/g, ' ').trim() || 'untitled'
    return item.link ? `${number} [${title}](<${item.link}>)` : `${number} ${title}`
  })

  const total = items.length
  const header = `📰 **${options.feedName}** — ${total} new article${total > 1 ? 's' : ''}:`
  const fullBody = lines.join('\n')

  if (header.length + 1 + fullBody.length <= LIMITS.content) {
    return [{ content: `${header}\n${fullBody}`, embeds: [] }]
  }

  // Split across multiple messages: header only on the first one.
  const messages: DigestMessage[] = []
  let currentLines: string[] = []
  let currentLength = 0

  for (const line of lines) {
    const lineLength = line.length + 1
    // The first message carries the header, so its body budget is smaller.
    const budget = messages.length === 0 ? LIMITS.content - header.length - 1 : LIMITS.content
    if (currentLength + lineLength > budget && currentLines.length > 0) {
      const isFirst = messages.length === 0
      messages.push({
        content: isFirst ? `${header}\n${currentLines.join('\n')}` : currentLines.join('\n'),
        embeds: [],
      })
      currentLines = []
      currentLength = 0
    }
    // A single line longer than a whole message gets truncated.
    if (line.length > LIMITS.content) {
      currentLines.push(`${line.slice(0, LIMITS.content - 3)}...`)
      currentLength += LIMITS.content
      continue
    }
    currentLines.push(line)
    currentLength += lineLength
  }
  if (currentLines.length > 0) {
    const isFirst = messages.length === 0
    messages.push({
      content: isFirst ? `${header}\n${currentLines.join('\n')}` : currentLines.join('\n'),
      embeds: [],
    })
  }

  return messages
}
