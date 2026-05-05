import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import DashboardTrafficModule from './DashboardTrafficModule.vue'
import { fetchSystemInfo, fetchTrafficOverview, fetchTrafficSummary } from '../../api'

const routerPush = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush })
}))

let trafficStatsEnabled = true
let overviewAgents = []
let overviewTrend = []
let overviewHostTrend = []
let overviewAgentsByRequest = null
let trafficSummaries = {}
let lastQueryClient = null

vi.mock('../../api', () => ({
  fetchSystemInfo: vi.fn(async () => ({ traffic_stats_enabled: trafficStatsEnabled })),
  fetchTrafficOverview: vi.fn(async (agentId, granularity) => {
    const agents = overviewAgentsByRequest?.[agentId || 'all'] ?? overviewAgents
    return {
      trend: overviewTrend,
      host_trend: overviewHostTrend,
      agents
    }
  }),
  fetchTrafficSummary: vi.fn(async (agentId) => trafficSummaries[agentId] ?? {
    http_rules: [{ scope_type: 'http_rule', scope_id: agentId, accounted_bytes: agentId === 'edge-3' ? 4096 : 1024 }],
    l4_rules: [],
    relay_listeners: []
  })
}))

const ApexChartStub = {
  name: 'apexchart',
  template: '<div data-testid="apexchart" />',
  props: ['type', 'options', 'series', 'height', 'width']
}

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
      plugins: [[VueQueryPlugin, { queryClient: createQueryClient() }]],
      stubs: {
        apexchart: ApexChartStub
      }
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
    overviewAgentsByRequest = null
    overviewTrend = []
    overviewHostTrend = []
    trafficSummaries = {}
    vi.clearAllMocks()
    routerPush.mockClear()
    vi.useRealTimers()
  })

  it('shows aggregate business traffic without quota bars when no quota is set', async () => {
    const wrapper = await mountModule()

    expect(wrapper.text()).toContain('节点分布')
    expect(wrapper.text()).toContain('3.00 KiB')
    expect(wrapper.text()).not.toContain('%')
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

  it('keeps top rules from different agents separate when rule ids overlap', async () => {
    trafficSummaries = {
      'edge-1': {
        http_rules: [{ scope_type: 'http_rule', scope_id: '1', accounted_bytes: 1024 }],
        l4_rules: [],
        relay_listeners: []
      },
      'edge-2': {
        http_rules: [{ scope_type: 'http_rule', scope_id: '1', accounted_bytes: 2048 }],
        l4_rules: [],
        relay_listeners: []
      }
    }

    const wrapper = await mountModule()
    await vi.waitFor(() => expect(fetchTrafficSummary).toHaveBeenCalledWith('edge-2'))

    const topRulesPanel = wrapper.find('.bento-card--top-rules')
    expect(topRulesPanel?.exists()).toBe(true)
    expect(topRulesPanel.text()).toContain('edge-1 / HTTP #1')
    expect(topRulesPanel.text()).toContain('edge-2 / HTTP #1')
    expect(topRulesPanel.text()).toContain('1.00 KiB')
    expect(topRulesPanel.text()).toContain('2.00 KiB')
    expect(topRulesPanel.findAll('.top-row')).toHaveLength(2)
  })

  it('navigates top rules using the complete hyphenated agent id', async () => {
    trafficSummaries = {
      'edge-1': {
        http_rules: [{ scope_type: 'http_rule', scope_id: '1', accounted_bytes: 1024 }],
        l4_rules: [],
        relay_listeners: []
      },
      'edge-2': {
        http_rules: [],
        l4_rules: [],
        relay_listeners: []
      }
    }

    const wrapper = await mountModule()
    await vi.waitFor(() => expect(fetchTrafficSummary).toHaveBeenCalledWith('edge-1'))

    await wrapper.find('.bento-card--top-rules .top-row').trigger('click')

    expect(routerPush).toHaveBeenCalledWith('/agents/edge-1')
  })

  it('does not pass host trend as a separate dashboard chart series', async () => {
    overviewTrend = [
      { bucket_start: '2026-05-01T00:00:00Z', rx_bytes: 10, tx_bytes: 20, accounted_bytes: 30 }
    ]
    overviewHostTrend = [
      { bucket_start: '2026-05-02T00:00:00Z', rx_bytes: 100, tx_bytes: 200, accounted_bytes: 300 }
    ]

    const wrapper = await mountModule()
    const chart = wrapper.findComponent({ name: 'TrafficTrendChart' })

    expect(chart.props('points')).toEqual([
      { bucket_start: '2026-05-01T00:00:00Z', rx_bytes: 10, tx_bytes: 20, accounted_bytes: 30 }
    ])
    expect(chart.props()).not.toHaveProperty('hostPoints')
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

    const quotaCard = wrapper.find('.bento-card--quota')
    expect(quotaCard?.exists()).toBe(true)
    expect(wrapper.text()).toContain('edge-1')
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
    await wrapper.vm.$.appContext.config.globalProperties.$queryClient?.invalidateQueries?.({ queryKey: ['traffic-overview', 'all', 'hour'] })
    await lastQueryClient.invalidateQueries({ queryKey: ['traffic-overview', 'all', 'hour'] })
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

    const cycleCard = wrapper.find('.bento-card--cycle')
    expect(cycleCard?.exists()).toBe(true)
    expect(cycleCard.text()).toContain('2026-05-01')
    expect(cycleCard.text()).toContain('入站')
  })

  it('keeps all agents available after filtering to one agent', async () => {
    overviewAgentsByRequest = {
      all: overviewAgents,
      'edge-1': [overviewAgents[0]]
    }
    const wrapper = await mountModule()
    const trigger = wrapper.find('.agent-picker__trigger')
    expect(trigger.exists()).toBe(true)

    await trigger.trigger('click')
    await nextTick()

    const items = wrapper.findAll('.agent-picker__item')
    const edge1Item = items.find(item => item.text().includes('edge-1'))
    expect(edge1Item).toBeTruthy()
    await edge1Item.trigger('click')
    await nextTick()

    await vi.waitFor(() => expect(fetchTrafficOverview).toHaveBeenCalledWith('edge-1', 'hour'))
    await nextTick()

    await trigger.trigger('click')
    await nextTick()

    const labels = wrapper.findAll('.agent-picker__item').map(item => item.text())
    expect(labels).toContain('全部节点')
    expect(labels).toContain('edge-1')
    expect(labels).toContain('edge-2')
  })

  it('shows mixed cycle label when aggregate agents have different cycle windows', async () => {
    overviewAgents = [
      {
        agent_id: 'edge-1',
        name: 'edge-1',
        used_bytes: 1024,
        quota_bytes: null,
        remaining_bytes: null,
        direction: 'both',
        cycle_start: '2026-05-01T00:00:00Z',
        cycle_end: '2026-06-01T00:00:00Z'
      },
      {
        agent_id: 'edge-2',
        name: 'edge-2',
        used_bytes: 2048,
        quota_bytes: null,
        remaining_bytes: null,
        direction: 'both',
        cycle_start: '2026-05-15T00:00:00Z',
        cycle_end: '2026-06-15T00:00:00Z'
      }
    ]

    const wrapper = await mountModule()

    const cycleCard = wrapper.find('.bento-card--cycle')
    expect(cycleCard?.exists()).toBe(true)
    expect(cycleCard.text()).toContain('多节点混合')
    expect(cycleCard.text()).not.toContain('2026-05-01')
  })
})
