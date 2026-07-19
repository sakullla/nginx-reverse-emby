// @vitest-environment node

import { effectScope, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentFilters as createAgentFilters } from '../hooks/useAgentFilters.js'

const routerState = vi.hoisted(() => ({
  route: { query: {} },
  router: { replace: vi.fn() }
}))

vi.mock('vue-router', () => ({
  useRoute: () => routerState.route,
  useRouter: () => routerState.router
}))

const activeScopes = []

function useAgentFilters(agents) {
  const scope = effectScope()
  activeScopes.push(scope)
  return scope.run(() => createAgentFilters(agents))
}

describe('useAgentFilters', () => {
  beforeEach(() => {
    activeScopes.length = 0
    routerState.route.query = {}
    routerState.router.replace.mockClear()
    localStorage.clear()
    vi.useFakeTimers()
    vi.setSystemTime('2026-06-28T10:05:00.000Z')
  })

  afterEach(() => {
    for (const scope of activeScopes) scope.stop()
    vi.useRealTimers()
  })

  it('falls back to monitor for unsupported route and persisted views', () => {
    for (const setup of [
      () => { routerState.route.query = { view: 'grid' } },
      () => { localStorage.setItem('agent-list-view', 'table') }
    ]) {
      routerState.route.query = {}
      localStorage.clear()
      setup()
      expect(useAgentFilters(ref([])).view.value).toBe('monitor')
    }
  })

  it('keeps monitor ordering stable inside a recency bucket', () => {
    const agents = ref([
      { id: 'b', last_seen_at: '2026-06-28T10:00:00.100Z', http_rules_count: 1 },
      { id: 'a', last_seen_at: '2026-06-28T10:00:00.050Z', http_rules_count: 1 },
      { id: 'c', last_seen_at: '2026-06-28T10:00:00.200Z', http_rules_count: 3 }
    ])
    const { filteredAgents } = useAgentFilters(agents)

    expect(filteredAgents.value.map(agent => agent.id)).toEqual(['c', 'a', 'b'])

    agents.value = [
      { id: 'b', last_seen_at: '2026-06-28T10:00:59.900Z', http_rules_count: 1 },
      { id: 'a', last_seen_at: '2026-06-28T10:00:30.050Z', http_rules_count: 1 },
      { id: 'c', last_seen_at: '2026-06-28T10:00:00.200Z', http_rules_count: 3 }
    ]
    expect(filteredAgents.value.map(agent => agent.id)).toEqual(['c', 'a', 'b'])
  })

  it('uses recency buckets in monitor view and exact time in list view', () => {
    const agents = ref([
      { id: 'old', last_seen_at: '2026-06-28T09:00:00.000Z' },
      { id: 'quarter', last_seen_at: '2026-06-28T09:55:00.000Z' },
      { id: 'recent', last_seen_at: '2026-06-28T10:04:30.000Z' },
      { id: 'few', last_seen_at: '2026-06-28T10:03:00.000Z' }
    ])
    expect(useAgentFilters(agents).filteredAgents.value.map(agent => agent.id)).toEqual([
      'recent', 'few', 'quarter', 'old'
    ])

    routerState.route.query = { view: 'list' }
    const exact = useAgentFilters(ref([
      { id: 'a', last_seen_at: '2026-06-28T10:03:00.000Z' },
      { id: 'b', last_seen_at: '2026-06-28T10:04:00.000Z' }
    ]))
    expect(exact.filteredAgents.value.map(agent => agent.id)).toEqual(['b', 'a'])
  })

  it('invalidates recency buckets as wall-clock time advances', async () => {
    const agents = ref([
      { id: 'a', last_seen_at: '2026-06-28T10:04:00.000Z' },
      { id: 'b', last_seen_at: '2026-06-28T10:04:30.000Z' }
    ])
    const { filteredAgents } = useAgentFilters(agents)

    expect(filteredAgents.value.map(agent => agent.id)).toEqual(['b', 'a'])
    await vi.advanceTimersByTimeAsync(90000)
    expect(filteredAgents.value.map(agent => agent.id)).toEqual(['a', 'b'])
  })

  it('reuses the result only while identities and order are unchanged', () => {
    const a = { id: 'a', last_seen_at: '2026-06-28T10:00:00.000Z' }
    const b = { id: 'b', last_seen_at: '2026-06-28T10:04:30.000Z' }
    const agents = ref([a, b])
    const { filteredAgents } = useAgentFilters(agents)
    const first = filteredAgents.value

    agents.value = [a, b]
    expect(filteredAgents.value).toBe(first)

    agents.value = [{ id: 'a', last_seen_at: '2026-06-28T10:04:45.000Z' }, b]
    expect(filteredAgents.value).not.toBe(first)
    expect(filteredAgents.value.map(agent => agent.id)).toEqual(['a', 'b'])
  })
})
