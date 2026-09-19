import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import DashboardPage from './DashboardPage.vue'

const attentionPayload = {
  ok: true,
  offline: { count: 1, agent_ids: ['a2'] },
  blocked: { count: 0, agent_ids: [] },
  sync_failed: { count: 0, agent_ids: [] },
  expiring_certs: { count: 2, items: [] },
  certs_total: 4
}

const { useCertificatesSpy, agentsState } = vi.hoisted(() => ({
  useCertificatesSpy: vi.fn(),
  agentsState: { list: [] }
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  RouterLink: { props: ['to'], template: '<a><slot /></a>' }
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: ref(agentsState.list), isLoading: ref(false) })
}))

vi.mock('../hooks/useAttention', () => ({
  useAttention: () => ({ data: ref(attentionPayload) })
}))

vi.mock('../hooks/useCertificates', () => ({
  useCertificates: useCertificatesSpy.mockReturnValue({ data: ref([]) })
}))

vi.mock('../api', () => ({
  fetchSystemInfo: vi.fn().mockResolvedValue({ traffic_stats_enabled: true })
}))

vi.mock('../components/dashboard/AttentionBar.vue', () => ({
  default: { name: 'AttentionBar', props: ['attention'], template: '<div data-testid="attention-bar" />' }
}))

vi.mock('../components/dashboard/ClusterMetricsCard.vue', () => ({
  default: {
    name: 'ClusterMetricsCard',
    props: ['agents', 'certsTotal', 'certsExpiring', 'defaultAgentId'],
    template: '<div data-testid="cluster-metrics" :data-certs-total="certsTotal" :data-certs-expiring="certsExpiring" />'
  }
}))

vi.mock('../components/dashboard/AgentStatusTiles.vue', () => ({
  default: {
    name: 'AgentStatusTiles',
    props: ['agents', 'detailed'],
    template: '<div data-testid="agent-tiles" :data-detailed="String(!!detailed)" />'
  }
}))

vi.mock('../components/traffic/DashboardTrafficModule.vue', () => ({
  default: { name: 'DashboardTrafficModule', template: '<div data-testid="traffic-module" />' }
}))

async function mountPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = mount(DashboardPage, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]],
      stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } }
    }
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  agentsState.list = [
    { id: 'a1', status: 'online' },
    { id: 'a2', status: 'offline' }
  ]
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('DashboardPage 健康优先布局', () => {
  it('健康总览(需关注条 + 集群指标 + 节点状态)排在流量模块之前', async () => {
    const html = (await mountPage()).html()
    const order = [
      'data-testid="attention-bar"',
      'data-testid="cluster-metrics"',
      'data-testid="agent-tiles"',
      'data-testid="traffic-module"'
    ].map((marker) => html.indexOf(marker))
    expect(order.every((idx) => idx >= 0), `missing section in: ${html}`).toBe(true)
    expect([...order].sort((a, b) => a - b)).toEqual(order)
    // 快捷操作已按用户反馈移除
    expect(html).not.toContain('快捷操作')
  })

  it('集群指标的证书数取自聚合接口,不再单独请求证书列表', async () => {
    const metrics = (await mountPage()).get('[data-testid="cluster-metrics"]')
    expect(metrics.attributes('data-certs-total')).toBe('4')
    expect(metrics.attributes('data-certs-expiring')).toBe('2')
    expect(useCertificatesSpy).not.toHaveBeenCalled()
  })

  it('少量节点(≤4)时健康网格切为紧凑布局,磁贴用详细行模式', async () => {
    agentsState.list = [{ id: 'local', status: 'online' }]
    const single = await mountPage()
    expect(single.get('.dashboard__health').classes()).toContain('dashboard__health--compact')
    expect(single.get('[data-testid="agent-tiles"]').attributes('data-detailed')).toBe('true')

    agentsState.list = Array.from({ length: 3 }, (_, i) => ({ id: `a${i}`, status: 'online' }))
    const few = await mountPage()
    expect(few.get('.dashboard__health').classes()).toContain('dashboard__health--compact')
    expect(few.get('[data-testid="agent-tiles"]').attributes('data-detailed')).toBe('true')
  })

  it('节点较多(>4)时维持宽栏密集磁贴布局', async () => {
    agentsState.list = Array.from({ length: 6 }, (_, i) => ({ id: `a${i}`, status: 'online' }))
    const wrapper = await mountPage()
    expect(wrapper.get('.dashboard__health').classes()).not.toContain('dashboard__health--compact')
    expect(wrapper.get('[data-testid="agent-tiles"]').attributes('data-detailed')).toBe('false')
  })
})
