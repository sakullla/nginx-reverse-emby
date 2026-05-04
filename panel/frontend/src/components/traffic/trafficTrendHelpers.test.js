import { describe, expect, it } from 'vitest'
import {
  alignSeriesByPosition,
  dateInputToRFC3339,
  hostTotalForLast24h,
  previousPeriodRange
} from './trafficTrendHelpers.mjs'

describe('trafficTrendHelpers', () => {
  it('converts date input values to RFC3339 range boundaries', () => {
    expect(dateInputToRFC3339('2026-05-04')).toBe('2026-05-04T00:00:00.000Z')
    expect(dateInputToRFC3339('2026-05-04', true)).toBe('2026-05-04T23:59:59.999Z')
    expect(dateInputToRFC3339('')).toBe('')
  })

  it('computes previous period boundaries in RFC3339 format', () => {
    expect(previousPeriodRange('2026-05-04', '2026-05-10')).toEqual({
      from: '2026-04-27T00:00:00.000Z',
      to: '2026-05-03T23:59:59.999Z'
    })
  })

  it('aligns comparison series by bucket position instead of timestamp equality', () => {
    const current = [
      { bucket_start: '2026-05-04T00:00:00Z' },
      { bucket_start: '2026-05-05T00:00:00Z' }
    ]
    const previous = [
      { bucket_start: '2026-04-28T00:00:00Z', accounted_bytes: 100 },
      { bucket_start: '2026-04-29T00:00:00Z', accounted_bytes: 200 }
    ]

    expect(alignSeriesByPosition(current, previous)).toEqual([100, 200])
  })

  it('sums only host trend points from the last 24 hours', () => {
    const now = new Date('2026-05-04T12:00:00.000Z')
    const points = [
      { bucket_start: '2026-05-03T11:00:00.000Z', accounted_bytes: 100 },
      { bucket_start: '2026-05-03T12:00:00.000Z', accounted_bytes: 200 },
      { bucket_start: '2026-05-04T00:00:00.000Z', accounted_bytes: 300 }
    ]

    expect(hostTotalForLast24h(points, now)).toBe(500)
  })
})
