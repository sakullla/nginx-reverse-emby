import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentFilters } from '../hooks/useAgentFilters.js'
import { getAgentStatus } from '../utils/agentHelpers.js'

const routerState = vi.hoisted(() => ({
  route: { query: {} },
  router: { replace: vi.fn() }
}))

vi.mock('vue-router', () => ({
  useRoute: () => routerState.route,
  useRouter: () => routerState.router
}))

describe('useAgentFilters helpers', () => {
  beforeEach(() => {
    routerState.route.query = {}
    routerState.router.replace.mockClear()
    localStorage.clear()
  })

  it('getAgentStatus works for filtering logic', () => {
    expect(getAgentStatus({ status: 'online' })).toBe('online')
    expect(getAgentStatus({ status: 'offline' })).toBe('offline')
    expect(getAgentStatus({ status: 'online', last_apply_status: 'failed' })).toBe('failed')
    expect(getAgentStatus({ status: 'online', desired_revision: 5, current_revision: 3 })).toBe('pending')
  })

  it('falls back to card view for unsupported route view values', () => {
    routerState.route.query = { view: 'grid' }
    localStorage.setItem('agent-list-view', 'list')

    const { view } = useAgentFilters(ref([]))

    expect(view.value).toBe('card')
  })

  it('falls back to card view for unsupported persisted view values', () => {
    routerState.route.query = {}
    localStorage.setItem('agent-list-view', 'table')

    const { view } = useAgentFilters(ref([]))

    expect(view.value).toBe('card')
  })
})
