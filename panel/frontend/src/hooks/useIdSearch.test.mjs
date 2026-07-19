// @vitest-environment node

import { describe, expect, it } from 'vitest'

import { exactIdItems, findAllMatchesInAgents } from './useIdSearch.js'

describe('exact ID pagination fallback', () => {
  it('materializes a resolved record outside the current page', () => {
    const record = { id: 57, frontend_url: 'https://media.example.com' }
    expect(exactIdItems({
      search: '#id=57',
      pageItems: [{ id: 1 }],
      resolvedMatch: { agentId: 'edge-1', record },
      agentFilter: 'edge-1'
    })).toEqual([{ ...record, agent_id: 'edge-1' }])
  })

  it('materializes a relay listener found outside the loaded page', () => {
    const listener = { id: 57, name: 'remote relay' }
    const [resolvedMatch] = findAllMatchesInAgents({
      relayListeners: [{ agentId: 'edge-2', listeners: [listener] }]
    }, '57')

    expect(exactIdItems({
      search: '#id=57',
      pageItems: [{ id: 1, name: 'unrelated' }],
      resolvedMatch,
      agentFilter: 'edge-2'
    })).toEqual([{ ...listener, agent_id: 'edge-2' }])
  })
})
