import { describe, expect, it } from 'vitest'
import {
  certCardStatusLabel,
  certCardStatusTone,
  enabledStatusLabel,
  enabledStatusTone,
  syncStatusLabel,
  syncStatusTone,
} from './resourceCardStatus.js'

describe('resourceCardStatus', () => {
  it('maps sync active to 生效中 / success', () => {
    expect(syncStatusLabel('active')).toBe('生效中')
    expect(syncStatusTone('active')).toBe('success')
  })

  it('maps sync disabled to 已禁用 / neutral', () => {
    expect(syncStatusLabel('disabled')).toBe('已禁用')
    expect(syncStatusTone('disabled')).toBe('neutral')
  })

  it('maps enabled flag for Relay/WG style cards', () => {
    expect(enabledStatusLabel(true)).toBe('生效中')
    expect(enabledStatusLabel(false)).toBe('已禁用')
    expect(enabledStatusTone(true)).toBe('success')
    expect(enabledStatusTone(false)).toBe('neutral')
  })

  it('maps cert issuing to warning data-status while label stays 签发中', () => {
    expect(certCardStatusTone({ enabled: true, status: 'issuing' })).toBe('warning')
    expect(certCardStatusLabel({ enabled: true, status: 'issuing' })).toBe('签发中')
  })

  it('maps disabled cert regardless of status', () => {
    expect(certCardStatusTone({ enabled: false, status: 'active' })).toBe('neutral')
    expect(certCardStatusLabel({ enabled: false, status: 'active' })).toBe('已禁用')
  })
})
