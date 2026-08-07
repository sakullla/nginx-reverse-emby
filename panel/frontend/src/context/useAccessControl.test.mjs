import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchCurrentActor } from '../api/access'
import { clearCredentials, setSessionToken } from '../api/authState'
import { useAccessControl } from './useAccessControl'

vi.mock('../api/access', () => ({
  fetchCurrentActor: vi.fn()
}))

describe('useAccessControl identity changes', () => {
  afterEach(() => {
    useAccessControl().clearActor()
    clearCredentials()
    vi.clearAllMocks()
  })

  it('clears loading when credentials change during a pending actor request', async () => {
    let resolveActor
    fetchCurrentActor.mockReturnValue(new Promise((resolve) => {
      resolveActor = resolve
    }))
    const access = useAccessControl()

    const pending = access.refreshActor()
    expect(access.loading.value).toBe(true)

    setSessionToken('replacement-session')
    expect(access.loading.value).toBe(false)

    resolveActor({ id: 'stale-actor', permissions: ['*'] })
    await expect(pending).resolves.toBeNull()
    expect(access.actor.value).toBeNull()
    expect(access.loading.value).toBe(false)
  })
})
