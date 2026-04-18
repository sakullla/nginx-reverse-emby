import test from 'node:test'
import assert from 'node:assert/strict'

import { reconcileSelectedAgent } from './agentSelection.js'

test('reconcileSelectedAgent preserves current selection while agents are still loading', () => {
  assert.deepEqual(
    reconcileSelectedAgent({
      currentSelectedAgentId: 'edge-agent-id',
      agents: undefined,
      systemInfo: null,
      systemInfoAttempted: false
    }),
    {
      nextSelectedAgentId: 'edge-agent-id',
      persist: false,
      clear: false
    }
  )
})

test('reconcileSelectedAgent falls back to local once agents are loaded and no valid selection exists', () => {
  assert.deepEqual(
    reconcileSelectedAgent({
      currentSelectedAgentId: null,
      agents: [{ id: 'local' }, { id: 'edge-agent-id' }],
      systemInfo: null,
      systemInfoAttempted: true
    }),
    {
      nextSelectedAgentId: 'local',
      persist: true,
      clear: false
    }
  )
})
