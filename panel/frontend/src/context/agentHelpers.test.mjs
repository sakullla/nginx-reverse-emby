import { describe, expect, it } from 'vitest'
import { getAgentStatus, getAgentStatusLabel, getModeLabel, getHostname, timeAgo } from '../utils/agentHelpers.js'

describe('getAgentStatus', () => {
  it('returns offline when agent is null', () => {
    expect(getAgentStatus(null)).toBe('offline')
  })

  it('returns offline when status is offline', () => {
    expect(getAgentStatus({ status: 'offline' })).toBe('offline')
  })

  it('returns failed when last_apply_status is failed', () => {
    expect(getAgentStatus({ status: 'online', last_apply_status: 'failed' })).toBe('failed')
  })

  it('returns pending when a newer desired revision is retrying after an older apply failure', () => {
    expect(getAgentStatus({
      status: 'online',
      desired_revision: 5,
      current_revision: 4,
      last_apply_revision: 4,
      last_apply_status: 'failed'
    })).toBe('pending')
  })

  it('returns failed when a failed apply has reached the desired revision', () => {
    expect(getAgentStatus({
      status: 'online',
      desired_revision: 5,
      current_revision: 4,
      last_apply_revision: 5,
      last_apply_status: 'failed'
    })).toBe('failed')
  })

  it('returns pending when desired_revision > current_revision', () => {
    expect(getAgentStatus({ status: 'online', desired_revision: 5, current_revision: 3 })).toBe('pending')
  })

  it('returns online otherwise', () => {
    expect(getAgentStatus({ status: 'online', desired_revision: 3, current_revision: 3 })).toBe('online')
  })
})

describe('getAgentStatusLabel', () => {
  it('maps status to Chinese labels', () => {
    expect(getAgentStatusLabel('online')).toBe('在线')
    expect(getAgentStatusLabel('offline')).toBe('离线')
    expect(getAgentStatusLabel('failed')).toBe('失败')
    expect(getAgentStatusLabel('pending')).toBe('同步中')
  })

  it('returns dash for unknown status', () => {
    expect(getAgentStatusLabel('unknown')).toBe('—')
  })
})

describe('getModeLabel', () => {
  it('maps mode to Chinese labels', () => {
    expect(getModeLabel('local')).toBe('本机')
    expect(getModeLabel('master')).toBe('主控')
    expect(getModeLabel('pull')).toBe('拉取')
  })

  it('returns pull for unknown mode', () => {
    expect(getModeLabel('unknown')).toBe('拉取')
  })
})

describe('getHostname', () => {
  it('extracts hostname from URL', () => {
    expect(getHostname('https://example.com:8080/path')).toBe('example.com')
  })

  it('returns empty string for invalid URL', () => {
    expect(getHostname('not-a-url')).toBe('')
  })

  it('returns empty string for empty input', () => {
    expect(getHostname('')).toBe('')
    expect(getHostname(null)).toBe('')
  })
})

describe('timeAgo', () => {
  it('returns — for null date', () => {
    expect(timeAgo(null)).toBe('—')
    expect(timeAgo(undefined)).toBe('—')
  })

  it('returns 刚刚 for recent dates', () => {
    const now = new Date()
    expect(timeAgo(now)).toBe('刚刚')
  })

  it('returns minutes for dates within an hour', () => {
    const fiveMinutesAgo = new Date(Date.now() - 5 * 60 * 1000)
    expect(timeAgo(fiveMinutesAgo)).toBe('5m')
  })
})
