// @vitest-environment node

import { describe, it, expect } from 'vitest'
import {
  usagePercent,
  dailyBudget,
  quotaColorThreshold,
  formatPercentage,
  summaryBucketForObject,
  agentTrafficBytes
} from './trafficStats.js'

describe('summaryBucketForObject', () => {
  const summary = {
    http_rules: [{ scope_id: '7', accounted_bytes: 3072 }],
    l4_rules: [{ scope_id: '9', accounted_bytes: 12288 }],
    relay_listeners: [{ scope_id: '11', accounted_bytes: 49152 }]
  }

  it.each([
    ['http_rules', 7, 3072],
    ['l4_rules', '9', 12288],
    ['relay_listeners', 11, 49152]
  ])('selects %s usage by normalized resource id', (mapName, id, accountedBytes) => {
    expect(summaryBucketForObject(summary, mapName, id)?.accounted_bytes).toBe(accountedBytes)
  })
})

describe('agentTrafficBytes', () => {
  const hostGib = 20 * 1024 ** 3
  const groupGib = 360 * 1024 ** 3

  it('prefers this agent accounted_bytes over group used_bytes', () => {
    expect(agentTrafficBytes({
      used_bytes: groupGib,
      accounted_bytes: hostGib,
      host_total: { accounted_bytes: hostGib, rx_bytes: hostGib / 2, tx_bytes: hostGib / 2 }
    })).toBe(hostGib)
  })

  it('falls back to host_total when accounted_bytes is absent', () => {
    expect(agentTrafficBytes({
      used_bytes: groupGib,
      host_total: { accounted_bytes: hostGib }
    })).toBe(hostGib)
  })

  it('falls back to used_bytes when agent accounted fields are absent', () => {
    expect(agentTrafficBytes({ used_bytes: 300 })).toBe(300)
  })

  it('keeps a real zero accounted total instead of substituting group used_bytes', () => {
    expect(agentTrafficBytes({
      used_bytes: groupGib,
      accounted_bytes: 0
    })).toBe(0)
  })
})

describe('usagePercent', () => {
  it('returns null for unlimited quota', () => {
    expect(usagePercent(100, null)).toBeNull()
  })
  it('treats zero quota as a real quota', () => {
    expect(usagePercent(0, 0)).toBe(0)
    expect(usagePercent(100, 0)).toBe(100)
  })
  it('computes percentage correctly', () => {
    expect(usagePercent(50, 100)).toBe(50)
    expect(usagePercent(120, 100)).toBe(100)
  })
})

describe('dailyBudget', () => {
  it('returns null for unlimited quota', () => {
    expect(dailyBudget(null, 30)).toBeNull()
  })
  it('divides quota by days', () => {
    expect(dailyBudget(3000, 30)).toBe(100)
  })
})

describe('quotaColorThreshold', () => {
  it('returns success below 70', () => {
    expect(quotaColorThreshold(50)).toBe('success')
  })
  it('returns warning at 70-89', () => {
    expect(quotaColorThreshold(75)).toBe('warning')
  })
  it('returns danger at 90+', () => {
    expect(quotaColorThreshold(95)).toBe('danger')
  })
  it('returns neutral for non-finite', () => {
    expect(quotaColorThreshold(null)).toBe('neutral')
  })
})

describe('formatPercentage', () => {
  it('formats finite numbers', () => {
    expect(formatPercentage(75)).toBe('75%')
  })
  it('returns fallback for non-finite', () => {
    expect(formatPercentage(null)).toBe('—')
    expect(formatPercentage(null, 'N/A')).toBe('N/A')
  })
})
