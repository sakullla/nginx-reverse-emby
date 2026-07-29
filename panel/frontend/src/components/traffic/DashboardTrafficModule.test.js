import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import DashboardTrafficModule from './DashboardTrafficModule.vue'
import { fetchSystemInfo, fetchTrafficAggregate } from '../../api'
import { __resetPreferenceCacheForTests } from '../../hooks/usePreference'

const routerPush = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush })
}))

let trafficStatsEnabled = true
let aggregateByRequest = {}
let agents = []
let lastQueryClient = null
const mountedWrappers = []

vi.mock('../../api', () => ({
  fetchSystemInfo: vi.fn(async () => ({ traffic_stats_enabled: trafficStatsEnabled })),
  fetchAgents: vi.fn(async () => agents.map((agent) => ({
    id: agent.agent_id,
    name: agent.name,
    status: agent.status || 'online',
    last_seen_at: agent.last_seen_at || '2026-05-20T00:00:00Z'
  }))),
  fetchTrafficAggregate: vi.fn(async (agentId, granularity) => {
    const key = agentId || 'all'
    const aggregate = aggregateByRequest[key] ?? buildAggregate(agentId, granularity)
    return {
      ...aggregate,
      agents: [...(aggregate.agents || [])],
      trend: [...(aggregate.trend || [])],
      category_trend: [...(aggregate.category_trend || [])],
      top_rules: [...(aggregate.top_rules || [])],
      top_nodes: [...(aggregate.top_nodes || [])]
    }
  })
}))

const ApexChartStub = {
  name: 'apexchart',
  template: '<div data-testid="apexchart" :data-series="JSON.stringify(series)" />',
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
  mountedWrappers.push(wrapper)
  await settleAsyncWork()
  return wrapper
}

async function settleAsyncWork() {
  await flushPromises()
  await nextTick()
  await flushPromises()
  await nextTick()
}

function buildAggregate(agentId = null, granularity = 'day') {
  const selectedAgents = agentId
    ? agents.filter((agent) => agent.agent_id === agentId)
    : agents
  const trend = [{
    bucket_start: '2026-05-01T00:00:00Z',
    rx_bytes: granularity === 'hour' ? 10 : 100,
    tx_bytes: 20,
    accounted_bytes: granularity === 'hour' ? 30 : 120
  }]
  return {
    ok: true,
    agents: selectedAgents,
    trend,
    category_trend: [
      {
        category: 'http_rule',
        points: trend.map((p) => ({ ...p, accounted_bytes: Math.round(p.accounted_bytes * 0.5), rx_bytes: Math.round(p.rx_bytes * 0.5), tx_bytes: Math.round(p.tx_bytes * 0.5) }))
      },
      {
        category: 'l4_rule',
        points: trend.map((p) => ({ ...p, accounted_bytes: Math.round(p.accounted_bytes * 0.3), rx_bytes: Math.round(p.rx_bytes * 0.3), tx_bytes: Math.round(p.tx_bytes * 0.3) }))
      },
      {
        category: 'relay_listener',
        points: trend.map((p) => ({ ...p, accounted_bytes: Math.round(p.accounted_bytes * 0.2), rx_bytes: Math.round(p.rx_bytes * 0.2), tx_bytes: Math.round(p.tx_bytes * 0.2) }))
      }
    ],
    top_nodes: selectedAgents.map((agent) => ({
      agent_id: agent.agent_id,
      name: agent.name,
      used_bytes: granularUsedBytes(agent.agent_id, granularity),
      quota_bytes: agent.quota_bytes
    })),
    top_rules: []
  }
}

function granularUsedBytes(agentId, granularity) {
  const values = {
    hour: { 'edge-1': 64, 'edge-2': 128 },
    day: { 'edge-1': 640, 'edge-2': 1280 },
    month: { 'edge-1': 6400, 'edge-2': 12800 }
  }
  return values[granularity]?.[agentId] ?? 0
}

describe('DashboardTrafficModule', () => {
  beforeEach(() => {
    localStorage.clear()
    __resetPreferenceCacheForTests()
    trafficStatsEnabled = true
    agents = [
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
        cycle_start: '2026-05-01T00:00:00Z',
        cycle_end: '2026-06-01T00:00:00Z'
      }
    ]
    aggregateByRequest = {}
    vi.clearAllMocks()
    routerPush.mockClear()
    vi.useRealTimers()
  })

  afterEach(() => {
    while (mountedWrappers.length) {
      mountedWrappers.pop().unmount()
    }
    lastQueryClient?.clear()
    lastQueryClient = null
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('does not fetch aggregate data when traffic stats are disabled', async () => {
    trafficStatsEnabled = false

    const wrapper = await mountModule()

    expect(fetchSystemInfo).toHaveBeenCalled()
    expect(fetchTrafficAggregate).not.toHaveBeenCalled()
    expect(wrapper.find('.dashboard-traffic').exists()).toBe(false)
  })

  it('loads the aggregate dashboard at day granularity by default', async () => {
    await mountModule()

    expect(fetchTrafficAggregate).toHaveBeenCalledWith(null, 'day')
  })

  it('renders the primary health KPIs from the default aggregate without blocked signals', async () => {
    const wrapper = await mountModule()

    expect(wrapper.find('[data-testid="health-kpi-grid"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="kpi-used"]').text()).toContain('3.00 KiB')
    expect(wrapper.find('[data-testid="kpi-remaining"]').text()).toBe('无限制')
    expect(wrapper.find('[data-testid="kpi-usage"]').text()).toBe('—')
    expect(wrapper.find('[data-testid="kpi-blocked"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="health-badge"]').text()).toBe('概览')
  })

  it('switches between node and rules views while keeping agent filter and driving TOP', async () => {
    aggregateByRequest.all = {
      ...buildAggregate(),
      category_trend: [
        {
          category: 'http_rule',
          points: [{ bucket_start: '2026-05-01T00:00:00Z', rx_bytes: 50, tx_bytes: 10, accounted_bytes: 60 }]
        },
        {
          category: 'l4_rule',
          points: [{ bucket_start: '2026-05-01T00:00:00Z', rx_bytes: 0, tx_bytes: 0, accounted_bytes: 0 }]
        },
        {
          category: 'relay_listener',
          points: [{ bucket_start: '2026-05-01T00:00:00Z', rx_bytes: 5, tx_bytes: 5, accounted_bytes: 10 }]
        }
      ],
      top_rules: [
        { agent_id: 'edge-1', key: 'edge-1:http_rule:1', scope_type: 'http_rule', scope_id: '1', label: 'edge-1 / HTTP #1', accounted_bytes: 1024 }
      ]
    }
    const wrapper = await mountModule()

    await vi.waitFor(() => expect(wrapper.find('[data-testid="health-kpi-grid"]').exists()).toBe(true))
    expect(wrapper.find('.dashboard-traffic__agent-picker').exists()).toBe(true)
    // 默认按节点：TOP 展示节点
    expect(wrapper.find('[data-testid="top-mode-label"]').text()).toBe('节点')
    expect(wrapper.find('.dt-top-item').exists()).toBe(true)
    expect(wrapper.find('.dt-top-rule').exists()).toBe(false)

    const rulesBtn = wrapper.findAll('[data-testid="trend-view-btn"]').find((btn) => btn.text() === '按规则')
    expect(rulesBtn).toBeTruthy()
    await rulesBtn.trigger('click')
    await nextTick()

    // 规则视角也保留节点筛选
    expect(wrapper.find('.dashboard-traffic__agent-picker').exists()).toBe(true)
    const chart = wrapper.findComponent({ name: 'TrafficTrendChart' })
    const seriesPoints = chart.props('seriesPoints')
    expect(Array.isArray(seriesPoints)).toBe(true)
    // 无数据的 L4 曲线应被忽略
    expect(seriesPoints.map((s) => s.name)).toEqual(['HTTP', 'Relay'])
    // TOP 跟随视角切到规则
    expect(wrapper.find('[data-testid="top-mode-label"]').text()).toBe('规则')
    expect(wrapper.find('.dt-top-rule').exists()).toBe(true)
    expect(wrapper.find('.dt-top-item').exists()).toBe(false)

    const nodesBtn = wrapper.findAll('[data-testid="trend-view-btn"]').find((btn) => btn.text() === '按节点')
    await nodesBtn.trigger('click')
    await nextTick()
    expect(wrapper.find('.dashboard-traffic__agent-picker').exists()).toBe(true)
    expect(wrapper.find('[data-testid="top-mode-label"]').text()).toBe('节点')
  })

  it('switches granularity and re-fetches aggregate', async () => {
    const wrapper = await mountModule()

    expect(fetchTrafficAggregate).toHaveBeenCalledWith(null, 'day')

    const hourBtn = wrapper.findAll('.dashboard-traffic__granularity-btn').find((btn) => btn.text() === '小时')
    expect(hourBtn).toBeTruthy()
    await hourBtn.trigger('click')
    await settleAsyncWork()

    expect(fetchTrafficAggregate).toHaveBeenCalledWith(null, 'hour')
  })

  it('renders overlapping top rules from different agents without duplicate Vue keys', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    aggregateByRequest.all = {
      ...buildAggregate(),
      top_rules: [
        { agent_id: 'edge-1', key: 'edge-1:http_rule:1', scope_type: 'http_rule', scope_id: '1', label: 'edge-1 / HTTP #1', accounted_bytes: 1024 },
        { agent_id: 'edge-2', key: 'edge-2:http_rule:1', scope_type: 'http_rule', scope_id: '1', label: 'edge-2 / HTTP #1', accounted_bytes: 2048 }
      ]
    }

    const wrapper = await mountModule()
    await vi.waitFor(() => expect(wrapper.find('[data-testid="trend-view-btn"]').exists()).toBe(true))
    const rulesBtn = wrapper.findAll('[data-testid="trend-view-btn"]').find((btn) => btn.text() === '按规则')
    await rulesBtn.trigger('click')
    await vi.waitFor(() => expect(wrapper.findAll('.dt-top-rule')).toHaveLength(2))

    const duplicateKeyWarning = warnSpy.mock.calls.some((args) =>
      args.some((arg) => String(arg).includes('Duplicate keys'))
    )
    expect(duplicateKeyWarning).toBe(false)
    expect(wrapper.text()).toContain('edge-1 / HTTP #1')
    expect(wrapper.text()).toContain('edge-2 / HTTP #1')

    warnSpy.mockRestore()
  })

  it('navigates top nodes using route params for reserved agent ids', async () => {
    agents = [
      {
        agent_id: 'edge/1',
        name: 'edge/1',
        used_bytes: 2048,
        quota_bytes: null,
        remaining_bytes: null,
        direction: 'both'
      },
      {
        agent_id: 'edge-2',
        name: 'edge-2',
        used_bytes: 1024,
        quota_bytes: null,
        remaining_bytes: null,
        direction: 'both'
      }
    ]

    const wrapper = await mountModule()
    // 默认按节点视角，TOP 直接显示节点
    await vi.waitFor(() => expect(wrapper.findAll('.dt-top-item')).toHaveLength(2))

    await wrapper.find('.dt-top-item').trigger('click')

    expect(routerPush).toHaveBeenCalledWith({
      name: 'agent-detail',
      params: { id: 'edge/1' }
    })
  })

  it('navigates top rules to their owning agent', async () => {
    aggregateByRequest.all = {
      ...buildAggregate(),
      top_rules: [
        { agent_id: 'edge/2', key: 'edge/2:http_rule:1', scope_type: 'http_rule', scope_id: '1', label: 'edge-2 / HTTP #1', accounted_bytes: 2048 }
      ]
    }

    const wrapper = await mountModule()
    await vi.waitFor(() => expect(wrapper.find('[data-testid="trend-view-btn"]').exists()).toBe(true))
    const rulesBtn = wrapper.findAll('[data-testid="trend-view-btn"]').find((btn) => btn.text() === '按规则')
    await rulesBtn.trigger('click')
    await vi.waitFor(() => expect(wrapper.find('.dt-top-rule').exists()).toBe(true))

    await wrapper.find('.dt-top-rule').trigger('click')

    expect(routerPush).toHaveBeenCalledWith({
      name: 'agent-detail',
      params: { id: 'edge/2' }
    })
  })

  it('keeps all agents available after filtering to one agent', async () => {
    aggregateByRequest = {
      all: buildAggregate(),
      'edge-1': {
        ...buildAggregate('edge-1'),
        agents: [agents[0]],
        top_nodes: [{
          agent_id: 'edge-1',
          name: 'edge-1',
          used_bytes: 1024,
          quota_bytes: null
        }]
      }
    }
    const wrapper = await mountModule()
    expect(fetchTrafficAggregate).toHaveBeenCalledWith(null, 'day')

    const trigger = wrapper.find('.agent-picker__trigger')
    await trigger.trigger('click')
    await nextTick()

    const findItems = () => Array.from(document.body.querySelectorAll('.agent-picker__item'))
    const edge1Item = findItems().find((item) => item.textContent.includes('edge-1'))
    expect(edge1Item).toBeTruthy()
    edge1Item.click()
    await settleAsyncWork()

    expect(fetchTrafficAggregate).toHaveBeenCalledWith('edge-1', 'day')
    await lastQueryClient.invalidateQueries({ queryKey: ['traffic-aggregate', 'edge-1', 'day'] })
    await settleAsyncWork()

    await trigger.trigger('click')
    await nextTick()

    const labels = findItems().map((item) => item.textContent)
    expect(labels).toContain('全部节点')
    expect(labels.some((l) => l.includes('edge-1'))).toBe(true)
    expect(labels.some((l) => l.includes('edge-2'))).toBe(true)
  })

  it('drives the traffic TOP card from the shared view switch', async () => {
    aggregateByRequest.all = {
      ...buildAggregate(),
      top_rules: [
        { agent_id: 'edge-1', key: 'edge-1:http_rule:1', scope_type: 'http_rule', scope_id: '1', label: 'edge-1 / HTTP #1', accounted_bytes: 1024 }
      ]
    }
    const wrapper = await mountModule()

    await vi.waitFor(() => expect(wrapper.find('.dt-top-item').exists()).toBe(true))
    expect(wrapper.find('.dt-top-rule').exists()).toBe(false)
    expect(wrapper.find('.dt-top-tab').exists()).toBe(false)

    const rulesBtn = wrapper.findAll('[data-testid="trend-view-btn"]').find((btn) => btn.text() === '按规则')
    await rulesBtn.trigger('click')
    await nextTick()

    expect(wrapper.find('.dt-top-rule').exists()).toBe(true)
    expect(wrapper.find('.dt-top-item').exists()).toBe(false)
  })
})
