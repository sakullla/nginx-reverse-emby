import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearAuthToken, setAuthToken } from './authState.js'
import { AGENT_MONITOR_STREAM_PATH, consumeAgentMonitorStream } from './agentMonitor.js'

function streamFromChunks(chunks) {
  return new ReadableStream({
    start(controller) {
      const encoder = new TextEncoder()
      chunks.forEach((chunk) => controller.enqueue(encoder.encode(chunk)))
      controller.close()
    }
  })
}

describe('consumeAgentMonitorStream', () => {
  afterEach(() => {
    clearAuthToken()
  })

  it('sends panel token and parses split NDJSON messages', async () => {
    setAuthToken('panel-secret')
    const fetchImpl = vi.fn(async () => ({
      ok: true,
      body: streamFromChunks([
        '{"type":"snapshot","payload":{"agents":[{"id":"edge-1"}]}}\n{"type":"upd',
        'ate","payload":{"agent":{"id":"edge-1","status":"online"}}}\n'
      ])
    }))
    const messages = []

    await consumeAgentMonitorStream({
      fetchImpl,
      onMessage: (message) => messages.push(message)
    })

    expect(fetchImpl).toHaveBeenCalledWith(AGENT_MONITOR_STREAM_PATH, expect.objectContaining({
      method: 'GET',
      headers: { 'X-Panel-Token': 'panel-secret' }
    }))
    expect(messages).toEqual([
      { type: 'snapshot', payload: { agents: [{ id: 'edge-1' }] } },
      { type: 'update', payload: { agent: { id: 'edge-1', status: 'online' } } }
    ])
  })

  it('throws a status error for failed stream responses', async () => {
    const fetchImpl = vi.fn(async () => ({ ok: false, status: 401 }))

    await expect(consumeAgentMonitorStream({ fetchImpl, onMessage: vi.fn() }))
      .rejects
      .toMatchObject({ status: 401 })
  })

  it('cancels the reader when message handling throws', async () => {
    const cancel = vi.fn(async () => {})
    const releaseLock = vi.fn()
    const encoder = new TextEncoder()
    const reader = {
      read: vi.fn()
        .mockResolvedValueOnce({
          value: encoder.encode('{"type":"snapshot","payload":{"agents":[]}}\n'),
          done: false
        })
        .mockResolvedValueOnce({ done: true }),
      cancel,
      releaseLock
    }
    const fetchImpl = vi.fn(async () => ({
      ok: true,
      body: { getReader: () => reader }
    }))

    await expect(consumeAgentMonitorStream({
      fetchImpl,
      onMessage: () => {
        throw new Error('bad consumer')
      }
    })).rejects.toThrow('bad consumer')

    expect(cancel).toHaveBeenCalledTimes(1)
    expect(releaseLock).toHaveBeenCalledTimes(1)
  })
})
