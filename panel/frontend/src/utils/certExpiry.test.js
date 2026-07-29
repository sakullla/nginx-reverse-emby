import { describe, it, expect } from 'vitest'
import { certExpiryInfo } from './certExpiry.js'

const NOW = Date.parse('2026-07-29T12:00:00Z')

describe('certExpiryInfo', () => {
  it('returns null for missing or invalid input', () => {
    expect(certExpiryInfo(null, NOW)).toBeNull()
    expect(certExpiryInfo('', NOW)).toBeNull()
    expect(certExpiryInfo('not-a-date', NOW)).toBeNull()
  })

  it('reports remaining days with success tone beyond 30 days', () => {
    const info = certExpiryInfo('2026-10-24T00:00:00Z', NOW)
    expect(info.expired).toBe(false)
    expect(info.daysLeft).toBe(86)
    expect(info.remainingLabel).toBe('剩余 86 天')
    expect(info.tone).toBe('success')
    expect(info.dateLabel).toBeTruthy()
  })

  it('uses warning tone within 30 days', () => {
    const info = certExpiryInfo('2026-08-20T00:00:00Z', NOW)
    expect(info.daysLeft).toBe(21)
    expect(info.tone).toBe('warning')
  })

  it('uses danger tone within 7 days', () => {
    const info = certExpiryInfo('2026-08-02T00:00:00Z', NOW)
    expect(info.daysLeft).toBe(3)
    expect(info.tone).toBe('danger')
  })

  it('falls back to hours under one day', () => {
    const info = certExpiryInfo('2026-07-29T17:30:00Z', NOW)
    expect(info.expired).toBe(false)
    expect(info.remainingLabel).toBe('剩余 5 小时')
    expect(info.tone).toBe('danger')
  })

  it('marks imminent expiry under one hour', () => {
    const info = certExpiryInfo('2026-07-29T12:30:00Z', NOW)
    expect(info.remainingLabel).toBe('即将到期')
  })

  it('reports expired certificates', () => {
    const info = certExpiryInfo('2026-07-26T12:00:00Z', NOW)
    expect(info.expired).toBe(true)
    expect(info.remainingLabel).toBe('已过期 3 天')
    expect(info.tone).toBe('danger')
  })

  it('reports just-expired certificates without a day count', () => {
    const info = certExpiryInfo('2026-07-29T11:00:00Z', NOW)
    expect(info.expired).toBe(true)
    expect(info.remainingLabel).toBe('已过期')
  })
})
