// @vitest-environment node

import { describe, expect, it } from 'vitest'
import { createNDJSONParser, mergeAgentsWithMonitor, mergeFetchedAgents, mergeMonitorAgents, monitorSnapshotAgents, quantizeLastSeenAt } from './agentMonitor.js'

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

  it('keeps the running package when a list refetch copies the target digest', () => {
    const running = 'a'.repeat(64)
    const target = 'b'.repeat(64)
    const previous = [{
      id: 'edge-1',
      runtime_package_sha256: running,
      desired_package_sha256: target,
      package_sync_status: 'pending'
    }]
    const next = [{
      id: 'edge-1',
      runtime_package_sha256: target,
      desired_package_sha256: target,
      package_sync_status: 'aligned'
    }]
    expect(mergeFetchedAgents(previous, next)).toEqual([{
      id: 'edge-1',
      runtime_package_sha256: running,
      desired_package_sha256: target,
      package_sync_status: 'pending'
    }])
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

    it('keeps the running package while staging heartbeats report the target digest', () => {
      const running = 'a'.repeat(64)
      const target = 'b'.repeat(64)
      const a = {
        id: 'edge-1',
        version: '1.0.0',
        runtime_package_version: '1.0.0',
        runtime_package_sha256: running,
        desired_package_sha256: target,
        package_sync_status: 'pending'
      }
      const monitor = {
        id: 'edge-1',
        version: '2.0.0',
        runtime_package_version: '2.0.0',
        runtime_package_sha256: target,
        desired_package_sha256: target,
        package_sync_status: 'aligned'
      }
      const merged = mergeAgentsWithMonitor([a], [monitor])
      expect(merged[0]).toMatchObject({
        version: '1.0.0',
        runtime_package_version: '1.0.0',
        runtime_package_sha256: running,
        desired_package_sha256: target,
        package_sync_status: 'pending'
      })
      expect(merged[0].monitor).toEqual(monitor)
    })

    it('accepts the new running package after the durable agent record has switched', () => {
      const target = 'b'.repeat(64)
      const a = {
        id: 'edge-1',
        version: '2.0.0',
        runtime_package_version: '2.0.0',
        runtime_package_sha256: target,
        desired_package_sha256: target,
        package_sync_status: 'aligned'
      }
      const merged = mergeAgentsWithMonitor([a], [{
        id: 'edge-1',
        version: '2.0.0',
        runtime_package_version: '2.0.0',
        runtime_package_sha256: target,
        desired_package_sha256: target,
        package_sync_status: 'aligned'
      }])
      expect(merged[0]).toMatchObject({
        version: '2.0.0',
        runtime_package_version: '2.0.0',
        runtime_package_sha256: target,
        package_sync_status: 'aligned'
      })
    })

    it('falls back to inline agent.monitor when no monitor entry matches', () => {
      const a = { id: 'a', monitor: { status: 'online' } }
      const merged = mergeAgentsWithMonitor([a], [])
      expect(merged[0]).toEqual({ id: 'a', status: 'online', monitor: { status: 'online' } })
      expect(merged[0]).not.toBe(a)
    })
  })
})
