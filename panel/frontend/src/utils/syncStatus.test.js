import { describe, expect, it } from 'vitest'
import { getAgentStatus } from './agentHelpers.js'
import { getAgentSyncStatus, getRuleEffectiveStatus } from './syncStatus.js'

const reportAckFailure = 'revision request /api/agent-revisions/478/report failed: 500 Internal Server Error: {"message":"internal server error","ok":false}'

describe('revision report ACK failures', () => {
  it('does not mark an already-applied agent as failed', () => {
    const agent = {
      status: 'online',
      current_revision: 478,
      last_apply_revision: 478,
      desired_revision: 1,
      last_apply_status: 'error',
      last_apply_message: reportAckFailure
    }
    expect(getAgentStatus(agent)).toBe('online')
    expect(getAgentSyncStatus(agent)).toBe('online')
  })

  it('still marks a real apply failure as failed', () => {
    const agent = {
      status: 'online',
      current_revision: 478,
      last_apply_revision: 478,
      last_apply_status: 'error',
      last_apply_message: 'nginx config test failed'
    }
    expect(getAgentStatus(agent)).toBe('failed')
    expect(getAgentSyncStatus(agent)).toBe('failed')
  })

  it('keeps already-applied rules active despite a sticky report ACK error', () => {
    const agent = {
      status: 'online',
      current_revision: 478,
      last_apply_revision: 478,
      last_apply_status: 'error',
      last_apply_message: reportAckFailure
    }
    expect(getRuleEffectiveStatus({ enabled: true, revision: 478 }, agent)).toBe('active')
  })
})
