import { describe, expect, it } from 'vitest'
import { ALL_AGENTS_FILTER } from '../utils/agentFilter.js'
import { reconcileSelectedAgent } from './agentSelection.js'

describe('reconcileSelectedAgent', () => {
  it('never preserves the all-agents filter as a concrete global selection', () => {
    expect(reconcileSelectedAgent({
      currentSelectedAgentId: ALL_AGENTS_FILTER,
      agents: [{ id: 'edge-1' }, { id: 'edge-2' }],
      systemInfo: { default_agent_id: 'edge-2' },
      systemInfoAttempted: true
    })).toEqual({
      nextSelectedAgentId: 'edge-2',
      persist: true,
      clear: false
    })
  })
})
