import { beforeEach, describe, expect, it } from 'vitest'

describe('authState', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('keeps shared token state in sync with localStorage', async () => {
    const mod = await import('./authState.js')

    mod.setAuthToken('panel-secret')
    expect(mod.authToken.value).toBe('panel-secret')
    expect(localStorage.getItem('panel_token')).toBe('panel-secret')

    mod.clearAuthToken()
    expect(mod.authToken.value).toBe(null)
    expect(localStorage.getItem('panel_token')).toBe(null)
  })
})
