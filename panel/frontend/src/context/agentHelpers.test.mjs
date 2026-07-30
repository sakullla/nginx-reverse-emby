// @vitest-environment node

import { describe, expect, it } from 'vitest'
import { getAgentStatus, getAgentStatusLabel, getModeLabel, getHostname, getAgentEndpointLabel, splitDdnsDomains, getAgentEndpointDisplay, timeAgo } from '../utils/agentHelpers.js'

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

describe('getAgentEndpointLabel', () => {
  it('prefers ddns_domain over agent_url hostname and last_seen_ip', () => {
    expect(getAgentEndpointLabel({
      ddns_domain: 'edge.example.com',
      agent_url: 'http://203.0.113.10:8080',
      last_seen_ip: '203.0.113.10'
    })).toBe('edge.example.com')
  })

  it('falls back to agent_url hostname when ddns is empty', () => {
    expect(getAgentEndpointLabel({
      ddns_domain: '',
      agent_url: 'http://edge-1.example.com:8080',
      last_seen_ip: '203.0.113.10'
    })).toBe('edge-1.example.com')
  })

  it('falls back to last_seen_ip when no domain or url host', () => {
    expect(getAgentEndpointLabel({
      last_seen_ip: '203.0.113.10'
    })).toBe('203.0.113.10')
  })

  it('returns dash when nothing is available', () => {
    expect(getAgentEndpointLabel(null)).toBe('—')
    expect(getAgentEndpointLabel({})).toBe('—')
  })
})

describe('splitDdnsDomains', () => {
  it('splits comma, Chinese comma, and newline separated domains', () => {
    expect(splitDdnsDomains('a.example.com, b.example.com，c.example.com\nd.example.com'))
      .toEqual(['a.example.com', 'b.example.com', 'c.example.com', 'd.example.com'])
  })

  it('trims blanks and drops duplicates while keeping first-seen order', () => {
    expect(splitDdnsDomains(' a.example.com ,, a.example.com, ,b.example.com '))
      .toEqual(['a.example.com', 'b.example.com'])
  })

  it('returns an empty list for non-string or empty input', () => {
    expect(splitDdnsDomains('')).toEqual([])
    expect(splitDdnsDomains(null)).toEqual([])
    expect(splitDdnsDomains(undefined)).toEqual([])
  })
})

describe('getAgentEndpointDisplay', () => {
  it('keeps a single domain as-is with no extra count', () => {
    expect(getAgentEndpointDisplay({ ddns_domain: 'edge.example.com' }))
      .toEqual({ primary: 'edge.example.com', extraCount: 0, full: 'edge.example.com' })
  })

  it('collapses multi-domain ddns configs to the first domain plus a count', () => {
    const display = getAgentEndpointDisplay({
      ddns_domain: '*.dmit.example.com, sub.example.com'
    })
    expect(display.primary).toBe('*.dmit.example.com')
    expect(display.extraCount).toBe(1)
    expect(display.full).toBe('*.dmit.example.com, sub.example.com')
  })

  it('falls back to host/IP with no extra count when ddns is empty', () => {
    const display = getAgentEndpointDisplay({ last_seen_ip: '203.0.113.10' })
    expect(display).toEqual({ primary: '203.0.113.10', extraCount: 0, full: '203.0.113.10' })
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
