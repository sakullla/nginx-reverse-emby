import { describe, expect, it } from 'vitest'
import { ALL_AGENTS_FILTER } from './agentFilter.js'
import {
  resolveCopyTargetAgentId,
  resolveCreateAgentId,
  resolveMutationAgentId,
} from './resolveResourceAgent.js'

const agents = [
  { id: 'local-1', name: 'Local', is_local: true },
  { id: 'edge-a', name: 'Edge A', is_local: false },
  { id: 'edge-b', name: 'Edge B', is_local: false },
]

describe('resolveCreateAgentId', () => {
  it('filter=agentA → create agentA', () => {
    const result = resolveCreateAgentId('edge-a', agents)
    expect(result).toMatchObject({
      agentId: 'edge-a',
      needsSelection: false,
      reason: 'concrete_filter',
    })
  })

  it('filter=all + multi agents → needs explicit selection', () => {
    const result = resolveCreateAgentId(ALL_AGENTS_FILTER, agents)
    expect(result.agentId).toBeNull()
    expect(result.needsSelection).toBe(true)
    expect(result.reason).toBe('multi_agent_all')
  })

  it('filter=all + only local → local', () => {
    const onlyLocal = [{ id: 'local-1', name: 'Local', is_local: true }]
    const result = resolveCreateAgentId(ALL_AGENTS_FILTER, onlyLocal)
    expect(result).toMatchObject({
      agentId: 'local-1',
      needsSelection: false,
      reason: 'single_local',
    })
  })

  it('filter=all + single non-local agent → that agent', () => {
    const one = [{ id: 'edge-a', name: 'Edge A', is_local: false }]
    const result = resolveCreateAgentId(ALL_AGENTS_FILTER, one)
    expect(result).toMatchObject({
      agentId: 'edge-a',
      needsSelection: false,
      reason: 'single_agent',
    })
  })

  it('explicitAgentId overrides all filter selection', () => {
    const result = resolveCreateAgentId(ALL_AGENTS_FILTER, agents, {
      explicitAgentId: 'edge-b',
    })
    expect(result).toMatchObject({
      agentId: 'edge-b',
      needsSelection: false,
      reason: 'explicit',
    })
  })

  it('missing filter with agents still needs selection', () => {
    const result = resolveCreateAgentId(null, agents)
    expect(result.agentId).toBeNull()
    expect(result.needsSelection).toBe(true)
    expect(result.reason).toBe('missing_filter')
  })
})

describe('resolveMutationAgentId', () => {
  it('mutation prefers resource.agent_id', () => {
    const result = resolveMutationAgentId(
      { id: 1, agent_id: 'edge-a' },
      ALL_AGENTS_FILTER,
    )
    expect(result).toMatchObject({
      agentId: 'edge-a',
      error: null,
      source: 'resource',
    })
  })

  it('falls back to concrete filter when resource lacks agent_id', () => {
    const result = resolveMutationAgentId({ id: 1 }, 'edge-b')
    expect(result).toMatchObject({
      agentId: 'edge-b',
      error: null,
      source: 'filter',
    })
  })

  it('uses fallbackAgentId before filter when provided', () => {
    const result = resolveMutationAgentId(
      { id: 1 },
      ALL_AGENTS_FILTER,
      { fallbackAgentId: 'local-1' },
    )
    expect(result).toMatchObject({
      agentId: 'local-1',
      error: null,
      source: 'fallback',
    })
  })

  it('returns perceptible error when agent cannot be resolved', () => {
    const result = resolveMutationAgentId({ id: 1 }, ALL_AGENTS_FILTER)
    expect(result.agentId).toBeNull()
    expect(result.error).toMatch(/缺少节点归属/)
    expect(result.source).toBe('none')
  })

  // Filter-bar list view may be ALL_AGENTS_FILTER, but mutation ownership
  // must stay resource-bound and never silently fall back to "write as all".
  it('keeps mutation ownership when list filter is all-agents', () => {
    const withOwner = resolveMutationAgentId(
      { id: 9, agent_id: 'edge-b' },
      ALL_AGENTS_FILTER,
    )
    expect(withOwner).toMatchObject({
      agentId: 'edge-b',
      error: null,
      source: 'resource',
    })

    const missingOwner = resolveMutationAgentId({ id: 9 }, ALL_AGENTS_FILTER)
    expect(missingOwner.agentId).toBeNull()
    expect(missingOwner.error).toMatch(/缺少节点归属/)
    expect(missingOwner.source).toBe('none')
  })
})

describe('resolveCopyTargetAgentId', () => {
  it('reuses create rules for copy target', () => {
    expect(resolveCopyTargetAgentId('edge-a', agents).agentId).toBe('edge-a')
    expect(resolveCopyTargetAgentId(ALL_AGENTS_FILTER, agents).needsSelection).toBe(true)
  })
})
