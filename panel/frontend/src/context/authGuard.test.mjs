import { beforeEach, describe, expect, it, vi } from 'vitest'

const verifyToken = vi.fn()
const fetchCurrentActor = vi.fn()

vi.mock('../api', () => ({ verifyToken }))
vi.mock('../api/access', () => ({ fetchCurrentActor }))

const { authGuard } = await import('../router/index.js')

describe('authGuard', () => {
  beforeEach(() => {
    localStorage.clear()
    verifyToken.mockReset()
    fetchCurrentActor.mockReset()
  })

  it('validates an active session without falling back to the bootstrap token', async () => {
    localStorage.setItem('panel_session', 'session-token')
    localStorage.setItem('panel_token', 'legacy-token')
    fetchCurrentActor.mockResolvedValue({ id: 'alice' })

    await expect(authGuard({ name: 'dashboard' })).resolves.toBe(true)
    expect(fetchCurrentActor).toHaveBeenCalledOnce()
    expect(verifyToken).not.toHaveBeenCalled()
  })

  it('clears every credential after an unauthorized session response', async () => {
    localStorage.setItem('panel_session', 'expired-session')
    localStorage.setItem('panel_token', 'legacy-token')
    fetchCurrentActor.mockRejectedValue({ response: { status: 401 } })

    await expect(authGuard({ name: 'dashboard' })).resolves.toEqual({ name: 'login' })
    expect(localStorage.getItem('panel_session')).toBeNull()
    expect(localStorage.getItem('panel_token')).toBeNull()
  })
})
