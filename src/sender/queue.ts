import type { APIEmbed } from 'discord.js'

export interface OutboundMessage {
  content: string
  embeds: APIEmbed[]
}

export type SendFn = (channelId: string, message: OutboundMessage) => Promise<void>

export interface SenderQueueOptions {
  /** Minimum delay between two messages to the same channel. */
  sendDelayMs: number
  /** Injected send function (bot client in prod, stub in tests). */
  send: SendFn
  /** Injectable timer for tests. */
  delay?: (ms: number) => Promise<void>
  onSendError?: (channelId: string, message: OutboundMessage, error: unknown) => void
}

interface ChannelQueue {
  messages: OutboundMessage[]
  running: boolean
}

/**
 * Per-channel FIFO sender. Preserves order, spaces out messages
 * (Discord: 5 msgs / 5 s per channel) and honors 429 retry_after
 * surfaced as errors with a `retryAfterMs` property.
 */
export class SenderQueue {
  private readonly queues = new Map<string, ChannelQueue>()
  private readonly delay: (ms: number) => Promise<void>

  constructor(private readonly options: SenderQueueOptions) {
    this.delay = options.delay ?? ((ms) => new Promise((resolve) => setTimeout(resolve, ms)))
  }

  /** Depth of the queue for a channel (0 if unknown). */
  depth(channelId: string): number {
    return this.queues.get(channelId)?.messages.length ?? 0
  }

  totalDepth(): number {
    let total = 0
    for (const queue of this.queues.values()) {
      total += queue.messages.length + (queue.running ? 1 : 0)
    }
    return total
  }

  /** Enqueue a message; delivery happens in the background. */
  send(channelId: string, message: OutboundMessage): void {
    let queue = this.queues.get(channelId)
    if (!queue) {
      queue = { messages: [], running: false }
      this.queues.set(channelId, queue)
    }
    queue.messages.push(message)
    if (!queue.running) {
      queue.running = true
      void this.pump(channelId, queue)
    }
  }

  private async pump(channelId: string, queue: ChannelQueue): Promise<void> {
    try {
      while (queue.messages.length > 0) {
        const message = queue.messages.shift() as OutboundMessage
        await this.deliver(channelId, message)
        if (queue.messages.length > 0) {
          await this.delay(this.options.sendDelayMs)
        }
      }
    } finally {
      queue.running = false
      // Messages may have been added while finishing; restart if needed.
      if (queue.messages.length > 0 && !queue.running) {
        queue.running = true
        void this.pump(channelId, queue)
      }
    }
  }

  private async deliver(channelId: string, message: OutboundMessage): Promise<void> {
    let attempt = 0
    while (true) {
      try {
        await this.options.send(channelId, message)
        return
      } catch (error) {
        const retryAfterMs = getRetryAfterMs(error)
        if (retryAfterMs !== null && attempt < 5) {
          attempt += 1
          await this.delay(retryAfterMs)
          continue
        }
        if (attempt < 2 && isTransientError(error)) {
          attempt += 1
          await this.delay(1000 * attempt)
          continue
        }
        this.options.onSendError?.(channelId, message, error)
        return
      }
    }
  }
}

interface RateLimitLike {
  retryAfterMs?: number
  status?: number
  code?: number
}

/** Extract retry_after (ms) from a discord.js rate limit error, if any. */
export function getRetryAfterMs(error: unknown): number | null {
  if (error === null || typeof error !== 'object') return null
  const candidate = error as RateLimitLike
  if (typeof candidate.retryAfterMs === 'number' && candidate.retryAfterMs >= 0) {
    return Math.ceil(candidate.retryAfterMs) + 100
  }
  return null
}

function isTransientError(error: unknown): boolean {
  if (error === null || typeof error !== 'object') return false
  const status = (error as RateLimitLike).status
  return typeof status === 'number' && status >= 500
}
