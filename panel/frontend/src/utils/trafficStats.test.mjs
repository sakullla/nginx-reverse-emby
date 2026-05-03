import { describe, it, expect } from 'vitest'
import { normalizeTrafficBucket, bucketForObject, formatBytes } from './trafficStats.js'

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
})
