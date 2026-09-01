import { beforeEach, describe, expect, it, vi } from 'vitest'
import { clearCredentials, setAuthToken, setSessionToken } from '../api/authState'

const verifyToken = vi.fn()
const fetchCurrentActor = vi.fn()

vi.mock('../api', () => ({ verifyToken }))
vi.mock('../api/access', () => ({ fetchCurrentActor }))

const { authGuard } = await import('../router/index.js')

describe('authGuard', () => {
  beforeEach(() => {
    clearCredentials()
    localStorage.clear()
    verifyToken.mockReset()
    fetchCurrentActor.mockReset()
  })

  it('rejects leftover panel_session without a panel token', async () => {
    setSessionToken('session-token')

    await expect(authGuard({ name: 'dashboard' })).resolves.toEqual({ name: 'login' })
    expect(fetchCurrentActor).not.toHaveBeenCalled()
    expect(verifyToken).not.toHaveBeenCalled()
  })

  it('validates a panel token and ignores leftover panel_session', async () => {
    setSessionToken('session-token')
    setAuthToken('panel-token')
    verifyToken.mockResolvedValue(true)

    await expect(authGuard({ name: 'dashboard' })).resolves.toBe(true)
    expect(verifyToken).toHaveBeenCalledWith('panel-token')
    expect(fetchCurrentActor).not.toHaveBeenCalled()
  })

  it('clears every credential after an unauthorized token response', async () => {
    setSessionToken('expired-session')
    setAuthToken('legacy-token')
    verifyToken.mockRejectedValue({ response: { status: 401 } })

    await expect(authGuard({ name: 'dashboard' })).resolves.toEqual({ name: 'login' })
    expect(localStorage.getItem('panel_session')).toBeNull()
    expect(localStorage.getItem('panel_token')).toBeNull()
    expect(fetchCurrentActor).not.toHaveBeenCalled()
  })

  it('sends an invalid panel token back to login', async () => {
    setAuthToken('bad-token')
    verifyToken.mockResolvedValue(false)

    await expect(authGuard({ name: 'dashboard' })).resolves.toEqual({ name: 'login' })
    expect(localStorage.getItem('panel_token')).toBeNull()
  })
})
