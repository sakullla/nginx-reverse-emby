import { describe, expect, it } from 'vitest'
import { createNDJSONParser, mergeAgentsWithMonitor, mergeMonitorAgents } from './agentMonitor.js'

describe('agent monitor utils', () => {
  it('parses split NDJSON chunks', () => {
    const messages = []
    const parser = createNDJSONParser((message) => messages.push(message))

    parser.push('{"type":"snap')
    parser.push('shot","payload":{"agents":[]}}\n{"type":"update"')
    parser.push(',"payload":{"agent":{"id":"edge-1"}}}\n')
    parser.flush()

    expect(messages).toEqual([
      { type: 'snapshot', payload: { agents: [] } },
      { type: 'update', payload: { agent: { id: 'edge-1' } } }
    ])
  })

  it('merges monitor updates by agent id', () => {
    expect(mergeMonitorAgents([{ id: 'edge-1', status: 'offline' }], {
      agent: { id: 'edge-1', status: 'online' }
    })).toEqual([{ id: 'edge-1', status: 'online' }])

    expect(mergeMonitorAgents([{ id: 'edge-1' }], {
      agent: { id: 'edge-2' }
    })).toEqual([{ id: 'edge-1' }, { id: 'edge-2' }])
  })

  describe('mergeAgentsWithMonitor', () => {
    it('returns the same array when no monitor data applies', () => {
      const agents = [{ id: 'a' }, { id: 'b' }]
      expect(mergeAgentsWithMonitor(agents, [])).toBe(agents)
      expect(mergeAgentsWithMonitor(agents, null)).toBe(agents)
      expect(mergeAgentsWithMonitor(agents, [{ id: 'c', status: 'online' }])).toBe(agents)
      expect(mergeAgentsWithMonitor(null, [])).toEqual([])
    })

    it('preserves agent object identity when no monitor data applies to that agent', () => {
      const a = { id: 'a' }
      const b = { id: 'b' }
      const merged = mergeAgentsWithMonitor([a, b], [{ id: 'c', status: 'online' }])
      expect(merged[0]).toBe(a)
      expect(merged[1]).toBe(b)
    })

    it('merges matching monitor data into the agent object', () => {
      const a = { id: 'a', name: 'A' }
      const merged = mergeAgentsWithMonitor([a], [{ id: 'a', status: 'online' }])
      expect(merged[0]).toEqual({ id: 'a', name: 'A', status: 'online', monitor: { id: 'a', status: 'online' } })
      expect(merged[0]).not.toBe(a)
    })

    it('falls back to inline agent.monitor when no monitor entry matches', () => {
      const a = { id: 'a', monitor: { status: 'online' } }
      const merged = mergeAgentsWithMonitor([a], [])
      expect(merged[0]).toEqual({ id: 'a', status: 'online', monitor: { status: 'online' } })
      expect(merged[0]).not.toBe(a)
    })
  })
})
