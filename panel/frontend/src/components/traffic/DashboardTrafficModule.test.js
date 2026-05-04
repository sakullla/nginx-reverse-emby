import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import DashboardTrafficModule from './DashboardTrafficModule.vue'
import { fetchSystemInfo, fetchTrafficOverview } from '../../api'

let trafficStatsEnabled = true

vi.mock('../../api', () => ({
  fetchSystemInfo: vi.fn(async () => ({ traffic_stats_enabled: trafficStatsEnabled })),
  fetchTrafficOverview: vi.fn(async () => ({
    trend: [],
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
    vi.clearAllMocks()
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
})
