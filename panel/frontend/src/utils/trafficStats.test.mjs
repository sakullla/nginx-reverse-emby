import { describe, it, expect } from 'vitest'
import {
  accountedBytes,
  formatBytes,
  formatQuota,
  normalizeTrafficBucket,
  normalizeTrafficPolicy,
  normalizeTrafficTrendPoint,
  normalizeTrafficTrendPoints,
  bucketForObject
} from './trafficStats.js'

describe('trafficStats', () => {
  it('normalizes null traffic buckets to zero totals', () => {
    expect(normalizeTrafficBucket(null)).toEqual({ rx_bytes: 0, tx_bytes: 0 })
  })

  it('looks up object buckets by stringified id', () => {
    const stats = {
      traffic: {
        http_rules: {
          11: { rx_bytes: 128, tx_bytes: 256 }
        }
      }
    }

    expect(bucketForObject(stats, 'http_rules', 11)).toEqual({ rx_bytes: 128, tx_bytes: 256 })
  })

  it('formats binary units compactly', () => {
    expect(formatBytes(1536)).toBe('1.50 KiB')
  })

  it('accounts by direction', () => {
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 }, 'rx')).toBe(10)
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 }, 'tx')).toBe(20)
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 }, 'both')).toBe(30)
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 }, 'max')).toBe(20)
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 })).toBe(30)
  })

  it('normalizes policy defaults', () => {
    expect(normalizeTrafficPolicy({}).direction).toBe('both')
    expect(normalizeTrafficPolicy({}).cycle_start_day).toBe(1)
  })

  it('normalizes policy retention and quota fields when present', () => {
    expect(normalizeTrafficPolicy({
      direction: 'sideways',
      cycle_start_day: 99,
      monthly_quota_bytes: -1,
      hourly_retention_days: '0',
      daily_retention_months: '12',
      monthly_retention_months: 'bad'
    })).toEqual({
      direction: 'both',
      cycle_start_day: 1,
      monthly_quota_bytes: null,
      block_when_exceeded: false,
      hourly_retention_days: 180,
      daily_retention_months: 12,
      monthly_retention_months: null
    })
  })

  it('normalizes monthly retention to nullable positive integers', () => {
    expect(normalizeTrafficPolicy({ monthly_retention_months: '6' }).monthly_retention_months).toBe(6)
    expect(normalizeTrafficPolicy({ monthly_retention_months: 1.5 }).monthly_retention_months).toBe(null)
    expect(normalizeTrafficPolicy({ monthly_retention_months: 0 }).monthly_retention_months).toBe(null)
    expect(normalizeTrafficPolicy({ monthly_retention_months: null }).monthly_retention_months).toBe(null)
  })

  it('formats quota values with an unlimited fallback', () => {
    expect(formatQuota(null)).toBe('Unlimited')
    expect(formatQuota(1536)).toBe('1.50 KiB')
  })

  it('normalizes trend points with accounted bytes', () => {
    expect(normalizeTrafficTrendPoint({
      bucket_start: '2026-05-03T00:00:00Z',
      rx_bytes: '10',
      tx_bytes: 20
    }, 'max')).toEqual({
      bucket_start: '2026-05-03T00:00:00Z',
      rx_bytes: 10,
      tx_bytes: 20,
      accounted_bytes: 20
    })
    expect(normalizeTrafficTrendPoints([{ rx_bytes: 1, tx_bytes: 2 }], 'rx')).toEqual([
      { bucket_start: '', rx_bytes: 1, tx_bytes: 2, accounted_bytes: 1 }
    ])
  })
})
