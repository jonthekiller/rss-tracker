import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getRetryAfterMs, type OutboundMessage, SenderQueue } from '../src/sender/queue.js'

function message(content: string): OutboundMessage {
  return { content, embeds: [] }
}

describe('SenderQueue', () => {
  let delays: number[]
  let delayImpl: (ms: number) => Promise<void>

  beforeEach(() => {
    delays = []
    delayImpl = async (ms) => {
      delays.push(ms)
    }
  })

  it('sends messages in order per channel', async () => {
    const sent: string[] = []
    const queue = new SenderQueue({
      sendDelayMs: 1000,
      send: async (channelId, msg) => {
        sent.push(`${channelId}:${msg.content}`)
      },
      delay: delayImpl,
    })
    queue.send('c1', message('a'))
    queue.send('c1', message('b'))
    queue.send('c2', message('x'))
    await vi.waitFor(() => {
      expect(sent).toHaveLength(3)
    })
    expect(sent).toEqual(['c1:a', 'c2:x', 'c1:b'])
    // 'c1:b' waited at least sendDelayMs after the previous c1 message.
    expect(delays).toContain(1000)
  })

  it('retries with retryAfterMs on rate limit errors', async () => {
    let attempts = 0
    const sent: string[] = []
    const queue = new SenderQueue({
      sendDelayMs: 0,
      send: async (_channelId, msg) => {
        attempts += 1
        if (attempts < 3) {
          throw Object.assign(new Error('rate limited'), { retryAfterMs: 250 })
        }
        sent.push(msg.content)
      },
      delay: delayImpl,
    })
    queue.send('c1', message('hello'))
    await vi.waitFor(() => {
      expect(sent).toEqual(['hello'])
    })
    expect(attempts).toBe(3)
    expect(delays).toContain(350) // 250 + 100 safety margin
  })

  it('calls onSendError after repeated failures', async () => {
    const errors: unknown[] = []
    const queue = new SenderQueue({
      sendDelayMs: 0,
      send: async () => {
        throw Object.assign(new Error('unknown channel'), { code: 10003 })
      },
      delay: delayImpl,
      onSendError: (_c, _m, error) => errors.push(error),
    })
    queue.send('c1', message('x'))
    await vi.waitFor(() => {
      expect(errors).toHaveLength(1)
    })
  })

  it('tracks depth', () => {
    const queue = new SenderQueue({
      sendDelayMs: 0,
      send: async () => {
        await new Promise((r) => setTimeout(r, 50))
      },
      delay: delayImpl,
    })
    queue.send('c1', message('1'))
    queue.send('c1', message('2'))
    expect(queue.depth('c1')).toBe(1)
    expect(queue.depth('c9')).toBe(0)
  })
})

describe('getRetryAfterMs', () => {
  it('extracts retryAfterMs with safety margin', () => {
    expect(getRetryAfterMs({ retryAfterMs: 1000 })).toBe(1100)
    expect(getRetryAfterMs(new Error('nope'))).toBeNull()
    expect(getRetryAfterMs(null)).toBeNull()
  })
})
