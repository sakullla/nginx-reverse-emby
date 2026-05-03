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
    monthly_quota_bytes: 1099511627776,
    remaining_bytes: 1099511627476,
    cycle_start: '2026-05-01T00:00:00Z',
    cycle_end: '2026-06-01T00:00:00Z',
    blocked: false
  })
  apiCalls.fetchTrafficTrend.mockResolvedValue([
    { bucket_start: '2026-05-01T00:00:00Z', rx_bytes: 100, tx_bytes: 200 }
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
    expect(wrapper.text()).toContain('月额度')
    expect(wrapper.text()).toContain('校准')
    expect(wrapper.text()).toContain('清理')
    expect(apiCalls.fetchTrafficPolicy).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficTrend).toHaveBeenCalledWith('edge-1', expect.objectContaining({ granularity: 'day' }))
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
    await nextTick()

    const quotaInput = wrapper.find('input[placeholder="留空表示无限制"]')
    await quotaInput.setValue('not-a-number')
    await wrapper.find('.traffic-panel__footer .btn-primary').trigger('click')

    expect(apiCalls.updateTrafficPolicy).not.toHaveBeenCalled()
  })

  it('does not normalize invalid traffic policy integers into defaults', async () => {
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await nextTick()

    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[0].setValue('99')
    await wrapper.find('.traffic-panel__footer .btn-primary').trigger('click')
    expect(apiCalls.updateTrafficPolicy).not.toHaveBeenCalled()

    await numberInputs[0].setValue('1')
    await numberInputs[1].setValue('0')
    await wrapper.find('.traffic-panel__footer .btn-primary').trigger('click')
    expect(apiCalls.updateTrafficPolicy).not.toHaveBeenCalled()

    await numberInputs[1].setValue('180')
    await numberInputs[2].setValue('0')
    await wrapper.find('.traffic-panel__footer .btn-primary').trigger('click')
    expect(apiCalls.updateTrafficPolicy).not.toHaveBeenCalled()
  })

  it('does not cleanup traffic history when confirmation is cancelled', async () => {
    window.confirm.mockReturnValue(false)
    const wrapper = await mountPage()
    await wrapper.findAll('.tab-btn').find((button) => button.text() === '流量统计').trigger('click')
    await nextTick()

    await wrapper.findAll('button').find((button) => button.text() === '清理').trigger('click')

    expect(apiCalls.cleanupTraffic).not.toHaveBeenCalled()
  })
})
