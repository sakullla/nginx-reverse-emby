import { describe, expect, it } from 'vitest'
import { buildProxyEntryAuthPayload } from './proxyEntryAuth'

describe('buildProxyEntryAuthPayload', () => {
  it('omits unchanged redacted auth so the stored password is preserved', () => {
    expect(buildProxyEntryAuthPayload(
      { enabled: true, username: 'client', password: '' },
      { enabled: true, username: 'client', password: '' }
    )).toBeUndefined()
  })

  it('sends explicit password changes', () => {
    expect(buildProxyEntryAuthPayload(
      { enabled: true, username: 'client', password: '' },
      { enabled: true, username: 'client', password: 'new-secret' }
    )).toEqual({
      enabled: true,
      username: 'client',
      password: 'new-secret',
    })
  })

  it('allows disabling auth without re-entering the redacted password', () => {
    expect(buildProxyEntryAuthPayload(
      { enabled: true, username: 'client', password: '' },
      { enabled: false, username: 'client', password: '' }
    )).toEqual({ enabled: false, username: '', password: '' })
  })

  it('requires re-entry before changing auth identity with a redacted password', () => {
    expect(() => buildProxyEntryAuthPayload(
      { enabled: true, username: 'client', password: '' },
      { enabled: true, username: 'other', password: '' }
    )).toThrow(/re-enter/)
  })
})
