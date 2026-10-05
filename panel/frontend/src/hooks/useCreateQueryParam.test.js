import { describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, reactive } from 'vue'
import { useCreateQueryParam } from './useCreateQueryParam'

describe('useCreateQueryParam', () => {
  it('opens the create form once and strips the temporary parameter', () => {
    const route = reactive({ query: { agentId: '1', create: '1' } })
    const router = { replace: vi.fn((target) => { route.query = target.query }) }
    const openCreate = vi.fn()
    const scope = effectScope()
    scope.run(() => useCreateQueryParam(route, router, openCreate))

    expect(openCreate).toHaveBeenCalledTimes(1)
    expect(router.replace).toHaveBeenCalledWith({ query: { agentId: '1' } })
    scope.stop()
  })

  it('does nothing when the parameter is absent', () => {
    const route = reactive({ query: { agentId: '1' } })
    const router = { replace: vi.fn() }
    const openCreate = vi.fn()
    const scope = effectScope()
    scope.run(() => useCreateQueryParam(route, router, openCreate))

    expect(openCreate).not.toHaveBeenCalled()
    expect(router.replace).not.toHaveBeenCalled()
    scope.stop()
  })

  it('opens again when the parameter returns', async () => {
    const route = reactive({ query: {} })
    const router = { replace: vi.fn((target) => { route.query = target.query }) }
    const openCreate = vi.fn()
    const scope = effectScope()
    scope.run(() => useCreateQueryParam(route, router, openCreate))

    route.query = { create: '1' }
    await nextTick()

    expect(openCreate).toHaveBeenCalledTimes(1)
    expect(router.replace).toHaveBeenCalledWith({ query: {} })
    scope.stop()
  })
})
