// @vitest-environment node

import { describe, expect, it } from 'vitest'

import {
  ALL_AGENTS_FILTER,
  isAllAgentsFilter,
  normalizeAgentFilter
} from './agentFilter.js'

describe('agentFilter', () => {
  it('exports a stable all-agents sentinel', () => {
    expect(ALL_AGENTS_FILTER).toBe('__all__')
    expect(isAllAgentsFilter(ALL_AGENTS_FILTER)).toBe(true)
    expect(isAllAgentsFilter('local')).toBe(false)
    expect(isAllAgentsFilter(null)).toBe(false)
  })

  it('normalizes blank values to null', () => {
    expect(normalizeAgentFilter(null)).toBe(null)
    expect(normalizeAgentFilter(undefined)).toBe(null)
    expect(normalizeAgentFilter('')).toBe(null)
    expect(normalizeAgentFilter('   ')).toBe(null)
  })

  it('maps historical all aliases to the sentinel', () => {
    expect(normalizeAgentFilter('__all__')).toBe(ALL_AGENTS_FILTER)
    expect(normalizeAgentFilter('all')).toBe(ALL_AGENTS_FILTER)
    expect(normalizeAgentFilter('*')).toBe(ALL_AGENTS_FILTER)
  })

  it('keeps concrete agent ids', () => {
    expect(normalizeAgentFilter('edge')).toBe('edge')
    expect(normalizeAgentFilter('  local  ')).toBe('local')
  })
})
