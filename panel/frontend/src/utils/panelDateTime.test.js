import { afterEach, describe, expect, it } from 'vitest'
import { formatPanelDateTime, getPanelTimeZone, setPanelTimeZone } from './panelDateTime.js'

describe('panelDateTime', () => {
  afterEach(() => setPanelTimeZone('UTC'))

  it('formats UTC instants in the panel timezone as YYYY/MM/DD HH:mm:ss', () => {
    setPanelTimeZone('Asia/Shanghai')
    expect(formatPanelDateTime('2026-08-24T12:06:27.499614715Z')).toBe('2026/08/24 20:06:27')
    expect(formatPanelDateTime('2026-08-24T06:55:53Z')).toBe('2026/08/24 14:55:53')
  })

  it('falls back to UTC when the timezone is empty or invalid', () => {
    setPanelTimeZone('')
    expect(getPanelTimeZone()).toBe('UTC')
    expect(formatPanelDateTime('2026-08-24T12:06:27Z')).toBe('2026/08/24 12:06:27')
    setPanelTimeZone('Not/AZone')
    expect(formatPanelDateTime('2026-08-24T12:06:27Z')).toBe('2026/08/24 12:06:27')
  })

  it('returns the empty placeholder for missing values and keeps unparseable text', () => {
    expect(formatPanelDateTime('')).toBe('—')
    expect(formatPanelDateTime(null, '')).toBe('')
    expect(formatPanelDateTime('not-a-date')).toBe('not-a-date')
  })

  it('keeps the panel timezone after /info loads so later renders use NRE_TIMEZONE', () => {
    expect(formatPanelDateTime('2026-08-24T06:55:53Z')).toBe('2026/08/24 06:55:53')
    setPanelTimeZone('Asia/Shanghai')
    expect(getPanelTimeZone()).toBe('Asia/Shanghai')
    expect(formatPanelDateTime('2026-08-24T06:55:53Z')).toBe('2026/08/24 14:55:53')
  })
})
