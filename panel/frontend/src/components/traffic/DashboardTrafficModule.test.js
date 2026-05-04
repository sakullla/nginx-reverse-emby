import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import DashboardTrafficModule from './DashboardTrafficModule.vue'
import { fetchSystemInfo, fetchTrafficOverview } from '../../api'

let trafficStatsEnabled = true
let hostTrend = []

vi.mock('../../api', () => ({
  fetchSystemInfo: vi.fn(async () => ({ traffic_stats_enabled: trafficStatsEnabled })),
  fetchTrafficOverview: vi.fn(async () => ({
    trend: [],
    host_trend: hostTrend,
    agents: [
      {
        agent_id: 'edge-1',
        name: 'edge-1',
        used_bytes: 1024,
        quota_bytes: null,
        remaining_bytes: null
      },
      {
        agent_id: 'edge-2',
        name: 'edge-2',
        used_bytes: 2048,
        quota_bytes: null,
        remaining_bytes: null
      }
    ]
  }))
}))

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false }
    }
  })
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
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('shows aggregate business traffic without quota bars when no quota is set', async () => {
    const wrapper = await mountModule()

    expect(wrapper.text()).toContain('业务流量')
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
})
