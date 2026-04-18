import { beforeEach, describe, expect, it, vi } from 'vitest'

const runtimeVerifyToken = vi.fn(async () => true)
const devRuntimeVerifyToken = vi.fn(async () => true)

vi.mock('./runtime.js', () => ({
  verifyToken: runtimeVerifyToken
}))

vi.mock('./devRuntime.js', () => ({
  verifyToken: devRuntimeVerifyToken
}))

describe('api facade', () => {
  beforeEach(() => {
    runtimeVerifyToken.mockClear()
    devRuntimeVerifyToken.mockClear()
  })

  it('delegates verifyToken to the selected runtime implementation', async () => {
    const api = await import('./index.js')

    const result = await api.verifyToken('panel-secret')

    expect(result).toBe(true)
    if (import.meta.env.DEV) {
      expect(devRuntimeVerifyToken).toHaveBeenCalledWith('panel-secret')
      expect(runtimeVerifyToken).not.toHaveBeenCalled()
      return
    }
    expect(runtimeVerifyToken).toHaveBeenCalledWith('panel-secret')
    expect(devRuntimeVerifyToken).not.toHaveBeenCalled()
  })

  it('does not expose the legacy token ref bridge', async () => {
    const api = await import('./index.js')

    expect('setApiTokenRef' in api).toBe(false)
  })
})
