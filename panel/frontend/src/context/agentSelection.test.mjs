import { describe, expect, it } from 'vitest'

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
})
