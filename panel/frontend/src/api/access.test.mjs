import { beforeEach, describe, expect, it, vi } from 'vitest'

const post = vi.fn()
vi.mock('./client', () => ({ api: { post } }))

const { login, logout } = await import('./access.js')

describe('session credential lifecycle', () => {
  beforeEach(() => {
    localStorage.clear()
    post.mockReset()
  })

  it('replaces a legacy panel token with the authenticated session', async () => {
    localStorage.setItem('panel_token', 'bootstrap-token')
    post.mockResolvedValue({ data: { session: { token: 'session-token' } } })

    await login('alice', 'password')

    expect(localStorage.getItem('panel_token')).toBeNull()
    expect(localStorage.getItem('panel_session')).toBe('session-token')
  })

  it('clears both credential modes even when server logout fails', async () => {
    localStorage.setItem('panel_token', 'bootstrap-token')
    localStorage.setItem('panel_session', 'session-token')
    post.mockRejectedValue(new Error('offline'))

    await expect(logout()).rejects.toThrow('offline')

    expect(localStorage.getItem('panel_token')).toBeNull()
    expect(localStorage.getItem('panel_session')).toBeNull()
  })
})
