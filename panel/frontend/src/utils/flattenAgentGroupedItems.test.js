// @vitest-environment node

import { describe, expect, it } from 'vitest'
import { flattenAgentGroupedItems } from './flattenAgentGroupedItems.js'

describe('flattenAgentGroupedItems', () => {
  it('returns empty for non-arrays', () => {
    expect(flattenAgentGroupedItems(null, 'rules')).toEqual([])
    expect(flattenAgentGroupedItems(undefined, 'rules')).toEqual([])
    expect(flattenAgentGroupedItems([], 'rules')).toEqual([])
  })

  it('passes through flat single-agent lists', () => {
    const rules = [{ id: 1, tags: ['a'] }, { id: 2, tags: ['b'] }]
    expect(flattenAgentGroupedItems(rules, 'rules')).toEqual(rules)
  })

  it('flattens all-agents group payloads', () => {
    const grouped = [
      { agentId: 'a1', rules: [{ id: 1, tags: ['emby'] }, { id: 2, tags: ['web'] }] },
      { agentId: 'a2', rules: [{ id: 3, tags: ['media'] }] },
      { agentId: 'a3', rules: [] }
    ]
    expect(flattenAgentGroupedItems(grouped, 'rules')).toEqual([
      { id: 1, tags: ['emby'] },
      { id: 2, tags: ['web'] },
      { id: 3, tags: ['media'] }
    ])
  })

  it('supports certificates / listeners / l4Rules keys', () => {
    expect(flattenAgentGroupedItems(
      [{ agentId: 'x', certificates: [{ id: 9 }] }],
      'certificates'
    )).toEqual([{ id: 9 }])
    expect(flattenAgentGroupedItems(
      [{ agentId: 'x', listeners: [{ id: 4 }] }],
      'listeners'
    )).toEqual([{ id: 4 }])
    expect(flattenAgentGroupedItems(
      [{ agentId: 'x', l4Rules: [{ id: 5 }] }],
      'l4Rules'
    )).toEqual([{ id: 5 }])
  })
})
