import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import DashboardTrafficModule from './DashboardTrafficModule.vue'
import { fetchSystemInfo, fetchTrafficOverview, fetchTrafficSummary } from '../../api'

let trafficStatsEnabled = true
let hostTrend = []
let overviewAgents = []
let lastQueryClient = null

vi.mock('../../api', () => ({
  fetchSystemInfo: vi.fn(async () => ({ traffic_stats_enabled: trafficStatsEnabled })),
  fetchTrafficOverview: vi.fn(async () => ({
    trend: [],
    host_trend: hostTrend,
    agents: overviewAgents
  })),
  fetchTrafficSummary: vi.fn(async (agentId) => ({
    http_rules: [{ scope_type: 'http_rule', scope_id: agentId, accounted_bytes: agentId === 'edge-3' ? 4096 : 1024 }],
    l4_rules: [],
    relay_listeners: []
  }))
}))

function createQueryClient() {
  lastQueryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false }
    }
  })
  return lastQueryClient
}

async function mountModule() {
  const wrapper = mount(DashboardTrafficModule, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient: createQueryClient() }]]
    }
  })
  await nextTick()
  await vi.dynamicImportSettled()
  await nextTick()
  return wrapper
}

describe('DashboardTrafficModule', () => {
  beforeEach(() => {
    trafficStatsEnabled = true
    hostTrend = []
    overviewAgents = [
      {
        agent_id: 'edge-1',
        name: 'edge-1',
        used_bytes: 1024,
        quota_bytes: null,
        remaining_bytes: null,
        direction: 'both'
      },
      {
        agent_id: 'edge-2',
        name: 'edge-2',
        used_bytes: 2048,
        quota_bytes: null,
        remaining_bytes: null,
        direction: 'both'
      }
    ]
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('shows aggregate business traffic without quota bars when no quota is set', async () => {
    const wrapper = await mountModule()

    const businessCard = wrapper.findAll('.dashboard-traffic__card')
      .find(card => card.text().includes('业务流量'))
    expect(wrapper.text()).toContain('业务流量')
    expect(wrapper.text()).toContain('3.00 KiB')
    expect(businessCard?.text()).not.toContain('%')
    expect(wrapper.text()).toContain('Top 节点')
    expect(wrapper.text()).toContain('edge-1')
    expect(wrapper.text()).toContain('edge-2')
  })

  it('does not fetch traffic overview when traffic stats are disabled', async () => {
    trafficStatsEnabled = false

    const wrapper = await mountModule()

    expect(fetchSystemInfo).toHaveBeenCalled()
    expect(fetchTrafficOverview).not.toHaveBeenCalled()
    expect(wrapper.find('.dashboard-traffic').exists()).toBe(false)
  })

  it('shows only last 24h host traffic in dashboard card', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-05-04T12:00:00.000Z'))
    hostTrend = [
      { bucket_start: '2026-05-03T11:00:00.000Z', accounted_bytes: 1024 },
      { bucket_start: '2026-05-04T00:00:00.000Z', accounted_bytes: 2048 }
    ]

    const wrapper = await mountModule()

    const hostCard = wrapper.findAll('.dashboard-traffic__card')
      .find(card => card.text().includes('主机流量 (24h)'))
    expect(hostCard?.exists()).toBe(true)
    expect(hostCard.text()).toContain('2.00 KiB')
    expect(hostCard.text()).not.toContain('3.00 KiB')
  })

  it('shows zero quotas as real quota progress', async () => {
    overviewAgents = [{
      agent_id: 'edge-1',
      name: 'edge-1',
      used_bytes: 1024,
      quota_bytes: 0,
      remaining_bytes: -1024,
      direction: 'both'
    }]

    const wrapper = await mountModule()

    const businessCard = wrapper.findAll('.dashboard-traffic__card')
      .find(card => card.text().includes('业务流量'))
    expect(businessCard?.exists()).toBe(true)
    expect(businessCard.text()).toContain('100%')
    expect(wrapper.text()).toContain('edge-1')
    expect(wrapper.text()).toContain('100%')
  })

  it('refetches top rules when the all-node agent set changes', async () => {
    const wrapper = await mountModule()
    await vi.waitFor(() => expect(fetchTrafficSummary).toHaveBeenCalledWith('edge-1'))
    fetchTrafficSummary.mockClear()

    overviewAgents = [
      ...overviewAgents,
      {
        agent_id: 'edge-3',
        name: 'edge-3',
        used_bytes: 4096,
        quota_bytes: null,
        remaining_bytes: null,
        direction: 'both'
      }
    ]
    await wrapper.vm.$.appContext.config.globalProperties.$queryClient?.invalidateQueries?.({ queryKey: ['traffic-overview', 'all'] })
    await lastQueryClient.invalidateQueries({ queryKey: ['traffic-overview', 'all'] })
    await wrapper.vm.$nextTick()
    await vi.dynamicImportSettled()

    await vi.waitFor(() => expect(fetchTrafficSummary).toHaveBeenCalledWith('edge-3'))
  })

  it('renders cycle label from overview agent cycle fields', async () => {
    overviewAgents = [{
      agent_id: 'edge-1',
      name: 'edge-1',
      used_bytes: 1024,
      quota_bytes: null,
      remaining_bytes: null,
      direction: 'rx',
      cycle_start: '2026-05-01T00:00:00Z',
      cycle_end: '2026-06-01T00:00:00Z'
    }]

    const wrapper = await mountModule()

    const cycleCard = wrapper.findAll('.dashboard-traffic__card')
      .find(card => card.text().includes('计费周期'))
    expect(cycleCard?.exists()).toBe(true)
    expect(cycleCard.text()).toContain('2026-05-01')
    expect(cycleCard.text()).toContain('入站')
  })
})
