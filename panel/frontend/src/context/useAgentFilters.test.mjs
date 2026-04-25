import { describe, expect, it } from 'vitest'
import { getAgentStatus } from '../utils/agentHelpers.js'

describe('useAgentFilters helpers', () => {
  it('getAgentStatus works for filtering logic', () => {
    expect(getAgentStatus({ status: 'online' })).toBe('online')
    expect(getAgentStatus({ status: 'offline' })).toBe('offline')
    expect(getAgentStatus({ status: 'online', last_apply_status: 'failed' })).toBe('failed')
    expect(getAgentStatus({ status: 'online', desired_revision: 5, current_revision: 3 })).toBe('pending')
  })
})
