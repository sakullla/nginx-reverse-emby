import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeTrafficBucket, bucketForObject, formatBytes } from './trafficStats.js'

test('normalizeTrafficBucket returns zero totals for null input', () => {
  assert.deepEqual(normalizeTrafficBucket(null), { rx_bytes: 0, tx_bytes: 0 })
})

test('bucketForObject looks up traffic buckets by stringified object id', () => {
  const stats = {
    traffic: {
      http_rules: {
        11: { rx_bytes: 128, tx_bytes: 256 }
      }
    }
  }

  assert.deepEqual(bucketForObject(stats, 'http_rules', 11), { rx_bytes: 128, tx_bytes: 256 })
})

test('formatBytes formats binary units compactly', () => {
  assert.equal(formatBytes(1536), '1.50 KiB')
})
