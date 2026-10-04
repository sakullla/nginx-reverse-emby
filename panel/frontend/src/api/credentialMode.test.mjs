import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('../../api/client', () => ({ api: { post } }))

import { login, logout } from '../../api/access'
import {
  authToken,
  sessionToken,
  setAuthToken,
  setSessionToken
} from '../../api/authState'

describe('credential mode exclusivity', () => {
  beforeEach(() => {
    localStorage.clear()
    authToken.value = null
    sessionToken.value = null
    post.mockReset()
  })

  it('clears a legacy bootstrap token before storing a user session', async () => {
    setAuthToken('legacy-administrator')
    post.mockResolvedValue({ data: { session: { token: 'user-session' } } })

    await login('alice', 'password')

    expect(authToken.value).toBe(null)
    expect(sessionToken.value).toBe('user-session')
    expect(localStorage.getItem('panel_token')).toBe(null)
  })

  it('clears both credential modes after logout', async () => {
    setAuthToken('legacy-administrator')
    setSessionToken('user-session')
    post.mockResolvedValue({ data: { ok: true } })

    await logout()

    expect(authToken.value).toBe(null)
    expect(sessionToken.value).toBe(null)
  })
})
