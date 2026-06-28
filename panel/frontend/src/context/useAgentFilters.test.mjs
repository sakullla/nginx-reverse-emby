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

  it('falls back to monitor view for unsupported route view values', () => {
    routerState.route.query = { view: 'grid' }
    localStorage.setItem('agent-list-view', 'list')

    const { view } = useAgentFilters(ref([]))

    expect(view.value).toBe('monitor')
  })

  it('falls back to monitor view for unsupported persisted view values', () => {
    routerState.route.query = {}
    localStorage.setItem('agent-list-view', 'table')

    const { view } = useAgentFilters(ref([]))

    expect(view.value).toBe('monitor')
  })

  it('stabilizes last_seen_at sorting within the same minute using id tie-breaker', () => {
    const agents = ref([
      { id: 'b', last_seen_at: '2026-06-28T10:00:00.100Z' },
      { id: 'a', last_seen_at: '2026-06-28T10:00:00.050Z' },
      { id: 'c', last_seen_at: '2026-06-28T10:00:00.200Z' }
    ])
    const { filteredAgents } = useAgentFilters(agents)
    // Same minute → sorted by id ascending
    expect(filteredAgents.value.map(a => a.id)).toEqual(['a', 'b', 'c'])

    // Update milliseconds within the same minute; order should stay stable
    agents.value = [
      { id: 'b', last_seen_at: '2026-06-28T10:00:59.900Z' },
      { id: 'a', last_seen_at: '2026-06-28T10:00:30.050Z' },
      { id: 'c', last_seen_at: '2026-06-28T10:00:00.200Z' }
    ]
    expect(filteredAgents.value.map(a => a.id)).toEqual(['a', 'b', 'c'])
  })

  it('still reorders agents by last_seen_at across minute boundaries', () => {
    const agents = ref([
      { id: 'a', last_seen_at: '2026-06-28T10:00:00.000Z' },
      { id: 'b', last_seen_at: '2026-06-28T10:01:00.000Z' }
    ])
    const { filteredAgents } = useAgentFilters(agents)
    expect(filteredAgents.value.map(a => a.id)).toEqual(['b', 'a'])
  })
})
