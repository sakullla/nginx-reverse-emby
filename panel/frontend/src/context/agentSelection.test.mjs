// @vitest-environment node

import { describe, expect, it } from 'vitest'

import { ALL_AGENTS_FILTER } from '../utils/agentFilter.js'
import { reconcileSelectedAgent } from './agentSelection.js'

describe('reconcileSelectedAgent', () => {
  it('preserves current selection while agents are still loading', () => {
    expect(reconcileSelectedAgent({
      currentSelectedAgentId: 'edge-agent-id',
      agents: undefined,
      systemInfo: null,
      systemInfoAttempted: false
    })).toEqual({
      nextSelectedAgentId: 'edge-agent-id',
      persist: false,
      clear: false
    })
  })

  it('falls back to local once agents are loaded and no valid selection exists', () => {
    expect(reconcileSelectedAgent({
      currentSelectedAgentId: null,
      agents: [{ id: 'local' }, { id: 'edge-agent-id' }],
      systemInfo: null,
      systemInfoAttempted: true
    })).toEqual({
      nextSelectedAgentId: 'local',
      persist: true,
      clear: false
    })
  })

  it('preserves remembered all-agents sentinel when agents exist', () => {
    expect(reconcileSelectedAgent({
      currentSelectedAgentId: ALL_AGENTS_FILTER,
      agents: [{ id: 'local' }, { id: 'edge' }],
      systemInfo: { default_agent_id: 'local' },
      systemInfoAttempted: true
    })).toEqual({
      nextSelectedAgentId: ALL_AGENTS_FILTER,
      persist: false,
      clear: false
    })
  })

  it('does not auto-select all when there is no memory', () => {
    const result = reconcileSelectedAgent({
      currentSelectedAgentId: null,
      agents: [{ id: 'local' }, { id: 'edge' }],
      systemInfo: { default_agent_id: 'edge' },
      systemInfoAttempted: true
    })
    expect(result.nextSelectedAgentId).toBe('edge')
    expect(result.nextSelectedAgentId).not.toBe(ALL_AGENTS_FILTER)
  })

  it('falls back to default when stored agent id is no longer present', () => {
    expect(reconcileSelectedAgent({
      currentSelectedAgentId: 'deleted-node',
      agents: [{ id: 'local' }, { id: 'edge' }],
      systemInfo: { default_agent_id: 'local' },
      systemInfoAttempted: true
    })).toEqual({
      nextSelectedAgentId: 'local',
      persist: true,
      clear: false
    })
  })

  it('clears selection when agents list is empty', () => {
    expect(reconcileSelectedAgent({
      currentSelectedAgentId: ALL_AGENTS_FILTER,
      agents: [],
      systemInfo: null,
      systemInfoAttempted: true
    })).toEqual({
      nextSelectedAgentId: null,
      persist: false,
      clear: true
    })
  })

  it('uses default_agent_id over local when selection is blank', () => {
    expect(reconcileSelectedAgent({
      currentSelectedAgentId: '',
      agents: [{ id: 'edge' }, { id: 'local' }],
      systemInfo: { default_agent_id: 'edge' },
      systemInfoAttempted: true
    }).nextSelectedAgentId).toBe('edge')
  })
})
