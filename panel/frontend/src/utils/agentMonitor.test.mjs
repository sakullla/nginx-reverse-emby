import { describe, expect, it } from 'vitest'
import { createNDJSONParser, mergeAgentsWithMonitor, mergeMonitorAgents, monitorSnapshotAgents, quantizeLastSeenAt } from './agentMonitor.js'

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

  describe('quantizeLastSeenAt', () => {
    it('rounds last_seen_at down to the minute', () => {
      const agent = { id: 'edge-1', last_seen_at: '2026-06-28T10:05:42.123Z' }
      expect(quantizeLastSeenAt(agent)).toEqual({
        id: 'edge-1',
        last_seen_at: '2026-06-28T10:05:00.000Z'
      })
    })

    it('returns the same object when last_seen_at is already on the minute', () => {
      const agent = { id: 'edge-1', last_seen_at: '2026-06-28T10:05:00.000Z' }
      expect(quantizeLastSeenAt(agent)).toBe(agent)
    })

    it('returns the same object when last_seen_at is missing', () => {
      const agent = { id: 'edge-1' }
      expect(quantizeLastSeenAt(agent)).toBe(agent)
    })
  })

  describe('monitorSnapshotAgents', () => {
    it('quantizes last_seen_at for each snapshot agent', () => {
      const snapshot = {
        agents: [
          { id: 'edge-1', last_seen_at: '2026-06-28T10:05:42.123Z' },
          { id: 'edge-2', last_seen_at: '2026-06-28T10:06:00.000Z' }
        ]
      }
      expect(monitorSnapshotAgents(snapshot)).toEqual([
        { id: 'edge-1', last_seen_at: '2026-06-28T10:05:00.000Z' },
        { id: 'edge-2', last_seen_at: '2026-06-28T10:06:00.000Z' }
      ])
    })
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
