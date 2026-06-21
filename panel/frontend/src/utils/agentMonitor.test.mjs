import { describe, expect, it } from 'vitest'
import { createNDJSONParser, mergeMonitorAgents } from './agentMonitor.js'

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
})
