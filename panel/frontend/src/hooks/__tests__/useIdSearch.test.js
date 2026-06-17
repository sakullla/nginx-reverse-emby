import { describe, it, expect } from 'vitest'
import { parseIdQuery, findRecordInAgents, findAllMatchesInAgents } from '../useIdSearch'

describe('parseIdQuery', () => {
  it('parses valid #id= query', () => {
    expect(parseIdQuery('#id=123')).toEqual({ isIdSearch: true, id: '123' })
    expect(parseIdQuery('#id=abc')).toEqual({ isIdSearch: true, id: 'abc' })
    expect(parseIdQuery('#id=hello-world')).toEqual({ isIdSearch: true, id: 'hello-world' })
  })

  it('handles whitespace trimming', () => {
    expect(parseIdQuery('  #id=123  ')).toEqual({ isIdSearch: true, id: '123' })
  })

  it('returns null for non-#id= input', () => {
    expect(parseIdQuery('keyword')).toBeNull()
    expect(parseIdQuery('name:test')).toBeNull()
    expect(parseIdQuery('#id=')).toBeNull() // no id value
    expect(parseIdQuery('#id= ')).toBeNull() // id is whitespace only
  })

  it('returns null for empty/null input', () => {
    expect(parseIdQuery('')).toBeNull()
    expect(parseIdQuery(null)).toBeNull()
    expect(parseIdQuery(undefined)).toBeNull()
  })

  it('does not match prefix #id=', () => {
    expect(parseIdQuery('#id=123 extra')).toBeNull() // \S+ doesn't match spaces
  })
})

describe('findRecordInAgents', () => {
  const mockData = {
    rules: [
      { agentId: 'agent-a', rules: [{ id: 1, frontend_url: 'a.com' }, { id: 2, frontend_url: 'b.com' }] },
      { agentId: 'agent-b', rules: [{ id: 3, frontend_url: 'c.com' }] }
    ],
    l4Rules: [
      { agentId: 'agent-a', l4Rules: [{ id: 10, protocol: 'tcp' }] }
    ],
    certificates: [
      { agentId: 'agent-b', certificates: [{ id: 20, domain: 'cert.com' }] }
    ],
    relayListeners: [
      { agentId: 'agent-a', listeners: [{ id: 30, name: 'relay1' }] }
    ]
  }

  it('finds rule by id', () => {
    const result = findRecordInAgents(mockData, '1')
    expect(result).toEqual({ agentId: 'agent-a', record: { id: 1, frontend_url: 'a.com' }, type: 'rule' })
  })

  it('finds rule in second agent', () => {
    const result = findRecordInAgents(mockData, '3')
    expect(result).toEqual({ agentId: 'agent-b', record: { id: 3, frontend_url: 'c.com' }, type: 'rule' })
  })

  it('finds l4 rule', () => {
    const result = findRecordInAgents(mockData, '10')
    expect(result).toEqual({ agentId: 'agent-a', record: { id: 10, protocol: 'tcp' }, type: 'l4' })
  })

  it('finds certificate', () => {
    const result = findRecordInAgents(mockData, '20')
    expect(result).toEqual({ agentId: 'agent-b', record: { id: 20, domain: 'cert.com' }, type: 'cert' })
  })

  it('finds relay listener', () => {
    const result = findRecordInAgents(mockData, '30')
    expect(result).toEqual({ agentId: 'agent-a', record: { id: 30, name: 'relay1' }, type: 'relay' })
  })

  it('returns null when not found', () => {
    expect(findRecordInAgents(mockData, '999')).toBeNull()
  })

  it('returns null for null data', () => {
    expect(findRecordInAgents(null, '1')).toBeNull()
  })

  it('type filter limits search scope', () => {
    // id=10 exists in l4Rules, not in rules
    expect(findRecordInAgents(mockData, '10', 'rule')).toBeNull()
    expect(findRecordInAgents(mockData, '10', 'l4')).toEqual({
      agentId: 'agent-a', record: { id: 10, protocol: 'tcp' }, type: 'l4'
    })
  })
})

describe('findAllMatchesInAgents', () => {
  it('finds all matches across types', () => {
    const data = {
      rules: [
        { agentId: 'a', rules: [{ id: 1 }] },
        { agentId: 'b', rules: [{ id: 1 }] }
      ],
      l4Rules: [],
      certificates: [],
      relayListeners: []
    }
    const results = findAllMatchesInAgents(data, '1')
    expect(results).toHaveLength(2)
    expect(results[0].agentId).toBe('a')
    expect(results[1].agentId).toBe('b')
  })

  it('returns empty array when no matches', () => {
    const data = { rules: [], l4Rules: [], certificates: [], relayListeners: [] }
    expect(findAllMatchesInAgents(data, '999')).toEqual([])
  })
})
