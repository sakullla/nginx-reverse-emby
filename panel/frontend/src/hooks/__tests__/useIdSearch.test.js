// @vitest-environment node

import { describe, expect, it } from 'vitest'
import {
  exactIdItems,
  findAllMatchesInAgents,
  findRecordInAgents,
  parseIdQuery,
  shouldStartCrossAgentIdSearch
} from '../useIdSearch'
import { ALL_AGENTS_FILTER } from '../../utils/agentFilter.js'

const records = {
  rules: [
    { agentId: 'agent-a', rules: [{ id: 1, frontend_url: 'a.com' }, { id: 2, frontend_url: 'b.com' }] },
    { agentId: 'agent-b', rules: [{ id: 3, frontend_url: 'c.com' }] }
  ],
  l4Rules: [{ agentId: 'agent-a', l4Rules: [{ id: 10, protocol: 'tcp' }] }],
  certificates: [{ agentId: 'agent-b', certificates: [{ id: 20, domain: 'cert.com' }] }],
  relayListeners: [{ agentId: 'agent-a', listeners: [{ id: 30, name: 'relay1' }] }]
}

describe('ID search helpers', () => {
  it('parses only a complete single-token #id query', () => {
    const valid = [
      ['#id=123', '123'],
      ['  #id=hello-world  ', 'hello-world']
    ]
    for (const [input, id] of valid) {
      expect(parseIdQuery(input)).toEqual({ isIdSearch: true, id })
    }
    for (const input of ['', null, undefined, 'keyword', '#id=', '#id=123 extra']) {
      expect(parseIdQuery(input)).toBeNull()
    }
  })

  it('finds each resource type across agents', () => {
    const expected = [
      ['1', 'agent-a', 'rule'],
      ['3', 'agent-b', 'rule'],
      ['10', 'agent-a', 'l4'],
      ['20', 'agent-b', 'cert'],
      ['30', 'agent-a', 'relay']
    ]
    for (const [id, agentId, type] of expected) {
      expect(findRecordInAgents(records, id)).toMatchObject({ agentId, type })
    }
  })

  it('honors type filters and missing data', () => {
    expect(findRecordInAgents(records, '10', 'rule')).toBeNull()
    expect(findRecordInAgents(records, '10', 'l4')).toMatchObject({ agentId: 'agent-a', type: 'l4' })
    expect(findRecordInAgents(records, '999')).toBeNull()
    expect(findRecordInAgents(null, '1')).toBeNull()
  })

  it('returns every same-ID match across agents', () => {
    const data = {
      rules: [
        { agentId: 'a', rules: [{ id: 1 }] },
        { agentId: 'b', rules: [{ id: 1 }] }
      ]
    }
    expect(findAllMatchesInAgents(data, '1').map(match => match.agentId)).toEqual(['a', 'b'])
    expect(findAllMatchesInAgents(data, '999')).toEqual([])
  })

  it('applies active filters to cross-agent matches', () => {
    const data = {
      rules: [
        { agentId: 'a', rules: [{ id: 1, enabled: false }] },
        { agentId: 'b', rules: [{ id: 1, enabled: true }] }
      ],
      certificates: [
        { agentId: 'a', certificates: [{ id: 2, enabled: true, status: 'pending' }] },
        { agentId: 'b', certificates: [{ id: 2, enabled: true, status: 'active' }] }
      ]
    }
    expect(findAllMatchesInAgents(data, '1', { enabled: true }).map(match => match.agentId)).toEqual(['b'])
    expect(findAllMatchesInAgents(data, '2', { enabled: true, status: 'pending' }).map(match => match.agentId)).toEqual(['a'])
  })

  it('starts cross-agent lookup only after a local miss finishes loading', () => {
    const base = { search: '#id=42', currentMatches: [], isLoading: false, isSearching: false }
    expect(shouldStartCrossAgentIdSearch(base)).toEqual({ isIdSearch: true, id: '42' })
    expect(shouldStartCrossAgentIdSearch({ ...base, isLoading: true })).toBeNull()
    expect(shouldStartCrossAgentIdSearch({ ...base, isSearching: true })).toBeNull()
    expect(shouldStartCrossAgentIdSearch({ ...base, currentMatches: [{ id: 42 }] })).toBeNull()
  })

  it('keeps normal results and materializes off-page exact matches', () => {
    const pageItems = [{ id: 10 }, { id: 20 }]
    expect(exactIdItems({ search: 'keyword', pageItems })).toEqual(pageItems)
    expect(exactIdItems({ search: '#id=20', pageItems })).toEqual([{ id: 20 }])

    const record = { id: 57, name: 'remote relay' }
    expect(exactIdItems({
      search: '#id=57',
      pageItems,
      resolvedMatch: { agentId: 'edge-2', record },
      agentFilter: 'edge-2'
    })).toEqual([{ ...record, agent_id: 'edge-2' }])
  })

  it('does not accept a current-page ID match before resolving all agents', () => {
    const pageItems = [{ id: 20, agent_id: 'edge-1' }]
    expect(exactIdItems({
      search: '#id=20',
      pageItems,
      agentFilter: ALL_AGENTS_FILTER
    })).toEqual([])
    expect(exactIdItems({
      search: '#id=20',
      pageItems,
      agentFilter: 'edge-1'
    })).toEqual(pageItems)
  })

  it('does not materialize a resolved record outside active filters', () => {
    const base = {
      search: '#id=57',
      pageItems: [],
      agentFilter: 'edge-2',
      resolvedMatch: { agentId: 'edge-2', record: { id: 57, enabled: false, status: 'pending' } }
    }
    expect(exactIdItems({ ...base, enabled: true })).toEqual([])
    expect(exactIdItems({ ...base, enabled: false, status: 'active' })).toEqual([])
    expect(exactIdItems({ ...base, enabled: false, status: 'pending' })).toHaveLength(1)
  })

  it('rejects unresolved, mismatched, and cross-agent exact records', () => {
    const base = { search: '#id=57', pageItems: [] }
    expect(exactIdItems(base)).toEqual([])
    expect(exactIdItems({
      ...base,
      resolvedMatch: { agentId: 'edge-2', record: { id: 58 } },
      agentFilter: 'edge-2'
    })).toEqual([])
    expect(exactIdItems({
      ...base,
      resolvedMatch: { agentId: 'edge-2', record: { id: 57 } },
      agentFilter: 'edge-1'
    })).toEqual([])
  })
})
