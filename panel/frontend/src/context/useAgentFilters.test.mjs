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

  it('sorts by total rule count within the same minute when last_seen_at is tied', () => {
    const agents = ref([
      { id: 'a', last_seen_at: '2026-06-28T10:00:00.000Z', http_rules_count: 1, l4_rules_count: 0 },
      { id: 'b', last_seen_at: '2026-06-28T10:00:00.000Z', http_rules_count: 3, l4_rules_count: 1 },
      { id: 'c', last_seen_at: '2026-06-28T10:00:00.000Z', http_rules_count: 2, l4_rules_count: 2 }
    ])
    const { filteredAgents } = useAgentFilters(agents)
    expect(filteredAgents.value.map(a => a.id)).toEqual(['b', 'c', 'a'])
  })

  it('falls back to id tie-breaker when last_seen_at and rule counts are equal', () => {
    const agents = ref([
      { id: 'b', last_seen_at: '2026-06-28T10:00:00.000Z', http_rules_count: 1, l4_rules_count: 1 },
      { id: 'a', last_seen_at: '2026-06-28T10:00:00.000Z', http_rules_count: 1, l4_rules_count: 1 }
    ])
    const { filteredAgents } = useAgentFilters(agents)
    expect(filteredAgents.value.map(a => a.id)).toEqual(['a', 'b'])
  })

  it('preserves filteredAgents array reference when sorted identities and order are unchanged', () => {
    const a = { id: 'a', last_seen_at: '2026-06-28T10:00:00.000Z' }
    const b = { id: 'b', last_seen_at: '2026-06-28T10:01:00.000Z' }
    const agents = ref([a, b])
    const { filteredAgents } = useAgentFilters(agents)
    const first = filteredAgents.value
    expect(first.map(x => x.id)).toEqual(['b', 'a'])

    // New source array but same object identities and same resulting order
    agents.value = [a, b]
    expect(filteredAgents.value).toBe(first)
  })

  it('returns a new filteredAgents array when sorted order changes', () => {
    const a = { id: 'a', last_seen_at: '2026-06-28T10:00:00.000Z' }
    const b = { id: 'b', last_seen_at: '2026-06-28T10:01:00.000Z' }
    const agents = ref([a, b])
    const { filteredAgents } = useAgentFilters(agents)
    const first = filteredAgents.value

    agents.value = [{ id: 'c', last_seen_at: '2026-06-28T10:02:00.000Z' }, a, b]
    expect(filteredAgents.value).not.toBe(first)
    expect(filteredAgents.value.map(x => x.id)).toEqual(['c', 'b', 'a'])
  })
})
