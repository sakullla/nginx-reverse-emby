import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import DashboardTrafficModule from './DashboardTrafficModule.vue'
import { fetchSystemInfo, fetchTrafficAggregate } from '../../api'

const routerPush = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush })
}))

let trafficStatsEnabled = true
let aggregateByRequest = {}
let agents = []
let lastQueryClient = null

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
      top_rules: [...(aggregate.top_rules || [])],
      top_nodes: [...(aggregate.top_nodes || [])]
    }
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

function buildAggregate(agentId = null, granularity = 'day') {
  const selectedAgents = agentId
    ? agents.filter((agent) => agent.agent_id === agentId)
    : agents
  return {
    ok: true,
    agents: selectedAgents,
    trend: [{
      bucket_start: '2026-05-01T00:00:00Z',
      rx_bytes: granularity === 'hour' ? 10 : 100,
      tx_bytes: 20,
      accounted_bytes: granularity === 'hour' ? 30 : 120
    }],
    top_nodes: selectedAgents.map((agent) => ({
      agent_id: agent.agent_id,
      name: agent.name,
      used_bytes: agent.used_bytes,
      quota_bytes: agent.quota_bytes
    })),
    top_rules: []
  }
}

describe('DashboardTrafficModule', () => {
  beforeEach(() => {
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

  it('does not fetch aggregate data when traffic stats are disabled', async () => {
    trafficStatsEnabled = false

    const wrapper = await mountModule()

    expect(fetchSystemInfo).toHaveBeenCalled()
    expect(fetchTrafficAggregate).not.toHaveBeenCalled()
    expect(wrapper.find('.dashboard-traffic').exists()).toBe(false)
  })

  it('loads the aggregate dashboard at day granularity by default', async () => {
    await mountModule()

    await vi.waitFor(() => expect(fetchTrafficAggregate).toHaveBeenCalledWith(null, 'day'))
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
    const rightCol = wrapper.findAll('.dashboard-traffic__col')[2]
    await vi.waitFor(() => expect(rightCol.findAll('.dt-top-item')).toHaveLength(2))

    await rightCol.find('.dt-top-item').trigger('click')

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
    await vi.waitFor(() => expect(fetchTrafficAggregate).toHaveBeenCalledWith(null, 'day'))

    const trigger = wrapper.find('.agent-picker__trigger')
    await trigger.trigger('click')
    await nextTick()

    const edge1Item = wrapper.findAll('.agent-picker__item').find((item) => item.text().includes('edge-1'))
    expect(edge1Item).toBeTruthy()
    await edge1Item.trigger('click')
    await nextTick()

    await vi.waitFor(() => expect(fetchTrafficAggregate).toHaveBeenCalledWith('edge-1', 'day'))
    await lastQueryClient.invalidateQueries({ queryKey: ['traffic-aggregate', 'edge-1', 'day'] })
    await vi.dynamicImportSettled()
    await nextTick()

    await trigger.trigger('click')
    await nextTick()

    const labels = wrapper.findAll('.agent-picker__item').map((item) => item.text())
    expect(labels).toContain('全部节点')
    expect(labels).toContain('edge-1')
    expect(labels).toContain('edge-2')
  })

  it('shows mixed cycle label when aggregate agents have different cycle windows', async () => {
    agents[1] = {
      ...agents[1],
      cycle_start: '2026-05-15T00:00:00Z',
      cycle_end: '2026-06-15T00:00:00Z'
    }

    const wrapper = await mountModule()

    await vi.waitFor(() => expect(wrapper.text()).toContain('多节点混合'))
    expect(wrapper.text()).not.toContain('计费周期2026-05-01')
  })
})
