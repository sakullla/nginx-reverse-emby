import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import AgentDetailPage from './AgentDetailPage.vue'

let routeParams
let systemInfo
let agentRecord
const apiCalls = {
  fetchTrafficPolicy: vi.fn(),
  fetchTrafficSummary: vi.fn(),
  fetchTrafficTrend: vi.fn(),
  updateTrafficPolicy: vi.fn(),
  calibrateTraffic: vi.fn(),
  cleanupTraffic: vi.fn()
}

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: routeParams }),
  useRouter: () => ({ push: vi.fn() }),
  RouterLink: {
    props: ['to'],
    template: '<a><slot /></a>'
  }
}))

vi.mock('../api', () => ({
  fetchAgentStats: vi.fn(async () => ({
    status: '正常',
    traffic: {
      total: { rx_bytes: 100, tx_bytes: 200 }
    }
  })),
  fetchSystemInfo: vi.fn(async () => systemInfo),
  fetchTrafficPolicy: (...args) => apiCalls.fetchTrafficPolicy(...args),
  updateTrafficPolicy: (...args) => apiCalls.updateTrafficPolicy(...args),
  fetchTrafficSummary: (...args) => apiCalls.fetchTrafficSummary(...args),
  fetchTrafficTrend: (...args) => apiCalls.fetchTrafficTrend(...args),
  calibrateTraffic: (...args) => apiCalls.calibrateTraffic(...args),
  cleanupTraffic: (...args) => apiCalls.cleanupTraffic(...args)
}))

vi.mock('../hooks/useAgents', async () => {
  const { ref } = await import('vue')
  return {
    useAgents: () => ({
      data: ref([agentRecord]),
      isLoading: ref(false)
    }),
    useUpdateAgent: () => ({
      isPending: ref(false),
      mutateAsync: vi.fn()
    })
  }
})

vi.mock('../hooks/useRules', async () => {
  const { ref } = await import('vue')
  return {
    useRules: () => ({ data: ref([]) })
  }
})

vi.mock('../hooks/useL4Rules', async () => {
  const { ref } = await import('vue')
  return {
    useL4Rules: () => ({ data: ref([]) })
  }
})

vi.mock('../hooks/useRelayListeners', async () => {
  const { ref } = await import('vue')
  return {
    useRelayListeners: () => ({ data: ref([]) })
  }
})

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false }
    }
  })
}

async function mountPage() {
  const wrapper = mount(AgentDetailPage, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient: createQueryClient() }]],
      stubs: {
        RouterLink: {
          props: ['to'],
          template: '<a><slot /></a>'
        }
      }
    }
  })
  await nextTick()
  await vi.dynamicImportSettled()
  await nextTick()
  return wrapper
}

beforeEach(() => {
  routeParams = { id: 'edge-1' }
  systemInfo = { traffic_stats_enabled: true }
  agentRecord = {
    id: 'edge-1',
    name: '边缘节点-01',
    agent_url: 'http://edge-1.example.com',
    last_seen_at: new Date().toISOString(),
    desired_revision: 1,
    current_revision: 1,
    last_apply_status: 'success',
    is_local: false
  }
  vi.restoreAllMocks()
  vi.clearAllMocks()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.spyOn(window, 'prompt').mockReturnValue(null)
  apiCalls.fetchTrafficPolicy.mockResolvedValue({
    direction: 'both',
    cycle_start_day: 1,
    monthly_quota_bytes: 1099511627776,
    block_when_exceeded: true,
    hourly_retention_days: 180,
    daily_retention_months: 24,
    monthly_retention_months: null
  })
  apiCalls.fetchTrafficSummary.mockResolvedValue({
    used_bytes: 300,
    policy: { direction: 'both' },
    aggregates: [
      { scope_type: 'http', scope_id: '', rx_bytes: 1024, tx_bytes: 2048, accounted_bytes: 3072 }
    ],
    http_rules: [
      { scope_type: 'http_rule', scope_id: '7', rx_bytes: 4096, tx_bytes: 8192, accounted_bytes: 12288 }
    ],
    l4_rules: [
      { scope_type: 'l4_rule', scope_id: '9', rx_bytes: 16384, tx_bytes: 32768, accounted_bytes: 49152 }
    ],
    relay_listeners: [
      { scope_type: 'relay_listener', scope_id: '11', rx_bytes: 65536, tx_bytes: 131072, accounted_bytes: 196608 }
    ],
    monthly_quota_bytes: 1099511627776,
    remaining_bytes: 1099511627476,
    cycle_start: '2026-05-01T00:00:00Z',
    cycle_end: '2026-06-01T00:00:00Z',
    blocked: false
  })
  apiCalls.fetchTrafficTrend.mockResolvedValue([
    { bucket_start: '2026-05-01T00:00:00Z', rx_bytes: 100, tx_bytes: 200, accounted_bytes: 300 },
    { bucket_start: '2026-05-02T00:00:00Z', rx_bytes: 10000, tx_bytes: 10000, accounted_bytes: 100 }
  ])
  apiCalls.updateTrafficPolicy.mockResolvedValue({})
  apiCalls.calibrateTraffic.mockResolvedValue({})
  apiCalls.cleanupTraffic.mockResolvedValue({})
})

describe('AgentDetailPage', () => {
  it('hides outbound proxy editing for embedded local agents', async () => {
    agentRecord.is_local = true
    const wrapper = await mountPage()

    await wrapper.findAll('.tab-btn').find((button) => button.text() === '系统信息').trigger('click')

    expect(wrapper.find('#agent-outbound-proxy').exists()).toBe(false)
  })

  it('renders traffic tab when traffic stats are enabled', async () => {
    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('流量统计')
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')

    expect(wrapper.text()).toContain('趋势')
    expect(wrapper.text()).toContain('额度')
    expect(wrapper.text()).toContain('策略设置')
    expect(wrapper.text()).toContain('历史管理')
    expect(wrapper.text()).toContain('方向')
    expect(wrapper.text()).toContain('双向')
    expect(apiCalls.fetchTrafficPolicy).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficTrend).toHaveBeenCalledWith('edge-1', expect.objectContaining({ granularity: 'day' }))
  })

  it('renders accounted traffic breakdowns in traffic tab', async () => {
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('分项流量')
    expect(wrapper.text()).toContain('HTTP')
    expect(wrapper.text()).toContain('HTTP 规则 #7')
    expect(wrapper.text()).toContain('12.0 KiB')

    const l4Tab = wrapper.findAll('.traffic-breakdown__tab').find((b) => b.text().includes('L4'))
    await l4Tab?.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('L4 规则 #9')
    expect(wrapper.text()).toContain('48.0 KiB')

    const relayTab = wrapper.findAll('.traffic-breakdown__tab').find((b) => b.text().includes('Relay'))
    await relayTab?.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('Relay 监听 #11')
    expect(wrapper.text()).toContain('192.0 KiB')
  })

  it('renders the traffic trend chart canvas', async () => {
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await nextTick()

    expect(wrapper.find('.traffic-trend-chart canvas').exists()).toBe(true)
  })

  it('switches traffic trend granularity', async () => {
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await nextTick()

    await wrapper.findAll('.traffic-trend__mode').find((button) => button.text() === '月').trigger('click')
    await nextTick()
    await vi.dynamicImportSettled()

    expect(apiCalls.fetchTrafficTrend).toHaveBeenLastCalledWith('edge-1', expect.objectContaining({ granularity: 'month' }))
  })

  it('hides traffic tab when traffic stats are disabled', async () => {
    systemInfo = { traffic_stats_enabled: false }

    const wrapper = await mountPage()

    expect(wrapper.findAll('.tab-btn').map((button) => button.text())).not.toContain('流量统计')
    expect(wrapper.text()).not.toContain('月额度')
    expect(apiCalls.fetchTrafficPolicy).not.toHaveBeenCalled()
    expect(apiCalls.fetchTrafficSummary).not.toHaveBeenCalled()
    expect(apiCalls.fetchTrafficTrend).not.toHaveBeenCalled()
  })

  it('does not submit invalid traffic policy values', async () => {
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await wrapper.findAll('.collapsible-section__header').find((button) => button.text().includes('策略设置')).trigger('click')
    await nextTick()

    const quotaInput = wrapper.find('input[placeholder="留空表示无限制"]')
    await quotaInput.setValue('not-a-number')
    await wrapper.find('.traffic-policy-form__footer .btn-primary').trigger('click')

    expect(apiCalls.updateTrafficPolicy).not.toHaveBeenCalled()
  })

  it('shows monthly quota with units and saves bytes', async () => {
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await wrapper.findAll('.collapsible-section__header').find((button) => button.text().includes('策略设置')).trigger('click')
    await nextTick()

    const quotaInput = wrapper.find('input[placeholder="留空表示无限制"]')
    const unitSelect = wrapper.find('.traffic-policy-form__unit')
    expect(quotaInput.element.value).toBe('1')
    expect(unitSelect.element.value).toBe('TiB')

    await quotaInput.setValue('1.5')
    await unitSelect.setValue('GiB')
    await wrapper.find('.traffic-policy-form__footer .btn-primary').trigger('click')

    expect(apiCalls.updateTrafficPolicy).toHaveBeenCalledWith('edge-1', expect.objectContaining({
      monthly_quota_bytes: 1610612736
    }))
  })

  it('does not normalize invalid traffic policy integers into defaults', async () => {
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await wrapper.findAll('.collapsible-section__header').find((button) => button.text().includes('策略设置')).trigger('click')
    await nextTick()

    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[0].setValue('99')
    await wrapper.find('.traffic-policy-form__footer .btn-primary').trigger('click')
    expect(apiCalls.updateTrafficPolicy).not.toHaveBeenCalled()

    await numberInputs[0].setValue('1')
    await numberInputs[1].setValue('0')
    await wrapper.find('.traffic-policy-form__footer .btn-primary').trigger('click')
    expect(apiCalls.updateTrafficPolicy).not.toHaveBeenCalled()

    await numberInputs[1].setValue('180')
    await numberInputs[2].setValue('0')
    await wrapper.find('.traffic-policy-form__footer .btn-primary').trigger('click')
    expect(apiCalls.updateTrafficPolicy).not.toHaveBeenCalled()
  })

  it('does not cleanup traffic history when confirmation is cancelled', async () => {
    window.confirm.mockReturnValue(false)
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await wrapper.findAll('.collapsible-section__header').find((button) => button.text().includes('历史管理')).trigger('click')
    await nextTick()

    await wrapper.findAll('button').find((button) => button.text() === '清理过期数据').trigger('click')

    expect(apiCalls.cleanupTraffic).not.toHaveBeenCalled()
  })

  it('calibrates traffic to a prompted byte value', async () => {
    window.prompt.mockReturnValue('1.5 GiB')
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await wrapper.findAll('.collapsible-section__header').find((button) => button.text().includes('历史管理')).trigger('click')
    await nextTick()

    await wrapper.findAll('button').find((button) => button.text() === '校准').trigger('click')

    expect(apiCalls.calibrateTraffic).toHaveBeenCalledWith('edge-1', {
      used_bytes: 1610612736
    })
  })

  it('calibrates traffic current usage to zero', async () => {
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await wrapper.findAll('.collapsible-section__header').find((button) => button.text().includes('历史管理')).trigger('click')
    await nextTick()

    await wrapper.findAll('button').find((button) => button.text() === '从现在归零').trigger('click')

    expect(apiCalls.calibrateTraffic).toHaveBeenCalledWith('edge-1', {
      used_bytes: 0
    })
  })
})
