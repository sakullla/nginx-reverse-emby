import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import AgentDetailPage from './AgentDetailPage.vue'
import AgentStatusBadge from '../components/AgentStatusBadge.vue'
import StatCard from '../components/base/StatCard.vue'

let routeParams
let systemInfo
let agentRecord
let currentAgentStats
let mockHttpRules = []
let mockL4Rules = []
let mockCertificates = []
let mockRelayListeners = []
const routerPush = vi.fn()
const apiCalls = {
  deleteAgent: vi.fn(),
  fetchTrafficPolicy: vi.fn(),
  fetchTrafficSummary: vi.fn(),
  fetchTrafficTrend: vi.fn(),
  updateTrafficPolicy: vi.fn(),
  calibrateTraffic: vi.fn(),
  cleanupTraffic: vi.fn(),
  updateAgent: vi.fn()
}

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: routeParams }),
  useRouter: () => ({ push: routerPush }),
  RouterLink: {
    props: ['to'],
    template: '<a><slot /></a>'
  }
}))

vi.mock('../components/base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: ['modelValue', 'title', 'size'],
    emits: ['update:modelValue', 'confirm'],
    template: '<div v-if="modelValue" class="modal-stub"><div class="modal-title">{{ title }}</div><slot /></div>'
  }
}))

vi.mock('../components/DeleteConfirmDialog.vue', () => ({
  default: {
    name: 'DeleteConfirmDialog',
    props: ['show', 'title', 'message', 'confirmText', 'loading'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" class="delete-dialog-stub"><div class="delete-dialog-title">{{ title }}</div><button class="delete-dialog-confirm" @click="$emit(\'confirm\')">{{ confirmText }}</button><button class="delete-dialog-cancel" @click="$emit(\'cancel\')">取消</button></div>'
  }
}))

vi.mock('../api', () => ({
  fetchAgentStats: vi.fn(async () => currentAgentStats),
  fetchSystemInfo: vi.fn(async () => systemInfo),
  fetchTrafficPolicy: (...args) => apiCalls.fetchTrafficPolicy(...args),
  updateTrafficPolicy: (...args) => apiCalls.updateTrafficPolicy(...args),
  fetchTrafficSummary: (...args) => apiCalls.fetchTrafficSummary(...args),
  fetchTrafficTrend: (...args) => apiCalls.fetchTrafficTrend(...args),
  calibrateTraffic: (...args) => apiCalls.calibrateTraffic(...args),
  cleanupTraffic: (...args) => apiCalls.cleanupTraffic(...args),
  deleteAgent: (...args) => apiCalls.deleteAgent(...args)
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
      mutateAsync: (...args) => apiCalls.updateAgent(...args)
    }),
    useDeleteAgent: () => ({
      isPending: ref(false),
      mutateAsync: (...args) => apiCalls.deleteAgent(...args)
    })
  }
})

vi.mock('../hooks/useRules', async () => {
  const { ref } = await import('vue')
  return {
    useRules: () => ({ data: ref(mockHttpRules) })
  }
})

vi.mock('../hooks/useL4Rules', async () => {
  const { ref } = await import('vue')
  return {
    useL4Rules: () => ({ data: ref(mockL4Rules) })
  }
})

vi.mock('../hooks/useRelayListeners', async () => {
  const { ref } = await import('vue')
  return {
    useRelayListeners: () => ({ data: ref(mockRelayListeners) })
  }
})

vi.mock('../hooks/useCertificates', async () => {
  const { ref } = await import('vue')
  return {
    useCertificates: () => ({ data: ref(mockCertificates) })
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
        },
        apexchart: {
          name: 'apexchart',
          template: '<div data-testid="apexchart" />'
        }
      }
    }
  })
  await nextTick()
  await vi.dynamicImportSettled()
  await nextTick()
  return wrapper
}

async function expandSection(wrapper, title) {
  const headers = wrapper.findAll('.collapsible-section__header')
  const target = headers.find((h) => h.text().includes(title))
  if (target) {
    await target.trigger('click')
    await nextTick()
    await vi.dynamicImportSettled()
    await nextTick()
  }
}

beforeEach(() => {
  routeParams = { id: 'edge-1' }
  systemInfo = { traffic_stats_enabled: true, master_register_token: 'test-token' }
  mockHttpRules = []
  mockL4Rules = []
  mockCertificates = []
  mockRelayListeners = []
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
  currentAgentStats = {
    status: '正常',
    traffic: {
      total: { rx_bytes: 100, tx_bytes: 200 }
    }
  }
  mockHttpRules = []
  mockL4Rules = []
  routerPush.mockClear()
  vi.restoreAllMocks()
  vi.clearAllMocks()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.spyOn(window, 'prompt').mockReturnValue(null)
  apiCalls.fetchTrafficPolicy.mockResolvedValue({
    direction: 'both',
    cycle_start_day: 1,
    monthly_quota_bytes: 1099511627776,
    block_when_exceeded: true,
    hourly_retention_days: 30,
    daily_retention_months: 3,
    monthly_retention_months: 36
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
  apiCalls.deleteAgent.mockResolvedValue({})
})

describe('AgentDetailPage', () => {
  it('renders status bar with name, status badge, mode badge and meta chips', async () => {
    agentRecord.status = 'online'
    agentRecord.mode = 'master'
    agentRecord.version = 'v1.2.3'
    agentRecord.tags = ['prod', 'edge', 'apac', 'extra']

    const wrapper = await mountPage()

    expect(wrapper.find('.agent-detail__summary-card').exists()).toBe(true)
    expect(wrapper.findComponent(AgentStatusBadge).exists()).toBe(true)
    expect(wrapper.text()).toContain('边缘节点-01')
    expect(wrapper.text()).toContain('主控')
    expect(wrapper.text()).toContain('v1.2.3')
    expect(wrapper.text()).toContain('prod')
    expect(wrapper.text()).toContain('+1')
  })

  it('renders resource metric tiles and count stat cards', async () => {
    currentAgentStats = {
      host: {
        cpu: { usage_percent: 12.4, used_cores: 1, total_cores: 8 },
        memory: { usage_percent: 63.8, used_bytes: 1024 * 1024 * 1024 * 10, total_bytes: 1024 * 1024 * 1024 * 16 },
        disk: { usage_percent: 77, used_bytes: 1024 * 1024 * 1024 * 398, total_bytes: 1024 * 1024 * 1024 * 512 },
        network: {
          total: { rx_bytes_per_second: 2048, tx_bytes_per_second: 1024 }
        }
      }
    }

    mockHttpRules = [{ id: 1, frontend_url: 'https://a.example.com', backends: [{ url: 'http://10.0.0.1:8080' }], enabled: true, tags: [] }]
    mockL4Rules = [{ id: 101, protocol: 'tcp', listen_host: '0.0.0.0', listen_port: 25565, backends: [{ host: '192.168.1.20', port: 25565 }], enabled: true, tags: [] }]

    const wrapper = await mountPage()

    const cpuTile = wrapper.find('[data-testid="detail-metric-cpu"]')
    const memoryTile = wrapper.find('[data-testid="detail-metric-memory"]')
    const diskTile = wrapper.find('[data-testid="detail-metric-disk"]')
    const networkTile = wrapper.find('[data-testid="detail-metric-network"]')

    expect(cpuTile.exists()).toBe(true)
    expect(memoryTile.exists()).toBe(true)
    expect(diskTile.exists()).toBe(true)
    expect(networkTile.exists()).toBe(true)

    // Resource occupancy uses center-percent rings; network stays rate rows.
    expect(cpuTile.find('[data-testid="agent-metric-tile-metric-ring"]').exists()).toBe(true)
    expect(memoryTile.find('[data-testid="agent-metric-tile-metric-ring"]').exists()).toBe(true)
    expect(diskTile.find('[data-testid="agent-metric-tile-metric-ring"]').exists()).toBe(true)
    expect(networkTile.find('[data-testid="agent-metric-tile-metric-ring"]').exists()).toBe(false)
    expect(networkTile.find('[data-testid="agent-metric-tile-network-down"]').exists()).toBe(true)
    expect(cpuTile.find('[data-testid="agent-metric-tile-ring-percent"]').text()).toContain('%')
    expect(cpuTile.find('[data-testid="agent-metric-tile-ring-value"]').text()).toMatch(/核|%/)

    // Sync status lives in the compact header badge row, not a loose primary-band block.
    const sync = wrapper.find('[data-testid="detail-sync-status"]')
    expect(sync.exists()).toBe(true)
    expect(sync.text()).toContain('同步状态')
    expect(wrapper.find('.base-list-card__header-left [data-testid="detail-sync-status"]').exists()).toBe(true)
    expect(wrapper.find('.agent-detail__primary-band [data-testid="detail-sync-status"]').exists()).toBe(false)
    expect(wrapper.find('.agent-detail__sync-signal').exists()).toBe(false)

    // Whole-page stack keeps summary + detail panels in one rhythm.
    expect(wrapper.find('.agent-detail__stack').exists()).toBe(true)
    expect(wrapper.find('.agent-detail__summary-card').classes()).toContain('agent-detail__panel')
    expect(wrapper.find('.agent-detail__detail-panels').exists()).toBe(true)

    // Address stays secondary under the title; metrics grid is the main body.
    expect(wrapper.find('.agent-detail__meta-row').exists()).toBe(true)
    expect(wrapper.find('.agent-detail__secondary-band').exists()).toBe(true)
    expect(wrapper.text()).toContain('资源与业务')
    expect(wrapper.find('.agent-detail__resource-metrics').classes()).toContain('agent-detail-metrics--aligned')
    expect(wrapper.find('.agent-detail__count-metrics').classes()).toContain('agent-detail-metrics--aligned')

    const statCards = wrapper.findAllComponents(StatCard)
    const labels = statCards.map((c) => c.props('label'))
    expect(labels).toContain('HTTP 规则')
    expect(labels).toContain('L4 规则')
    expect(labels).toContain('证书')
    expect(labels).toContain('Relay 监听')
    expect(labels).not.toContain('同步状态')

    // Resource tiles stay shallow-embedded; business counts are white raised + horizontal.
    // No fixed card height (cross-browser variance).
    expect(wrapper.find('.agent-detail__resource-metrics').classes()).toContain('agent-detail-metrics--embedded')
    expect(wrapper.find('.agent-detail__resource-metrics').classes()).not.toContain('agent-detail-metrics--fixed-height')
    expect(wrapper.find('.agent-detail__count-metrics').classes()).not.toContain('agent-detail-metrics--embedded')
    expect(wrapper.find('.agent-detail__count-metrics').classes()).not.toContain('agent-detail-metrics--fixed-height')
    expect(wrapper.find('.agent-detail__count-metrics').classes()).toContain('agent-detail-metrics--raised')
    expect(wrapper.find('.agent-detail__count-metrics').classes()).toContain('agent-detail-metrics--horizontal')

    const source = readFileSync(resolve(process.cwd(), 'src/pages/AgentDetailPage.vue'), 'utf8')
    expect(source).not.toContain('agent-detail-metrics--fixed-height')
    expect(source).not.toMatch(/height:\s*7\.5rem/)

    const embeddedStart = source.indexOf('.agent-detail-metrics--embedded')
    expect(embeddedStart).toBeGreaterThan(-1)
    const embeddedRule = source.slice(embeddedStart, embeddedStart + 450)
    expect(embeddedRule).toContain('color-dashboard-tile-bg')
    expect(embeddedRule).toContain('agent-metric-tile')
    expect(embeddedRule).not.toContain(':deep(.stat-card)')

    const raisedStart = source.indexOf('.agent-detail-metrics--raised')
    expect(raisedStart).toBeGreaterThan(-1)
    const raisedRule = source.slice(raisedStart, raisedStart + 500)
    expect(raisedRule).toContain('color-bg-surface')
    expect(raisedRule).toContain('shadow-sm')
    expect(raisedRule).toContain('stat-card')

    const horizontalStart = source.indexOf('.agent-detail-metrics--horizontal')
    expect(horizontalStart).toBeGreaterThan(-1)
    const horizontalRule = source.slice(horizontalStart, horizontalStart + 700)
    expect(horizontalRule).toContain('flex-direction: row')
    expect(horizontalRule).toContain('stat-card__icon')
    expect(horizontalRule).toContain('stat-card__data')
  })

  it('renders operation buttons in the summary header', async () => {
    const wrapper = await mountPage()
    expect(wrapper.find('[data-testid="detail-action-apply"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="detail-action-copy-join"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="detail-action-delete"]').exists()).toBe(true)
  })

  it('shows delete confirmation and deletes the agent', async () => {
    const wrapper = await mountPage()

    await wrapper.find('[data-testid="detail-action-delete"]').trigger('click')
    await nextTick()

    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)
    expect(wrapper.find('.delete-dialog-title').text()).toContain('确认删除节点')

    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await nextTick()

    expect(apiCalls.deleteAgent).toHaveBeenCalledWith('edge-1')
    expect(routerPush).toHaveBeenCalledWith('/agents')
  })

  it('disables delete button for local agents', async () => {
    agentRecord.is_local = true
    const wrapper = await mountPage()
    const deleteButton = wrapper.find('[data-testid="detail-action-delete"]')
    expect(deleteButton.element.disabled).toBe(true)
  })

  it('renders the rules section expanded by default and lists HTTP/L4 rules', async () => {
    mockHttpRules = [
      { id: 1, frontend_url: 'https://a.example.com', backends: [{ url: 'http://10.0.0.1:8080' }], enabled: true, tags: ['web', 'prod'] },
      { id: 2, frontend_url: 'https://b.example.com', backends: [{ url: 'http://10.0.0.2:8080' }], enabled: false, tags: [] }
    ]
    mockL4Rules = [
      { id: 101, protocol: 'tcp', listen_host: '0.0.0.0', listen_port: 25565, backends: [{ host: '192.168.1.20', port: 25565 }], enabled: true, tags: ['game'] }
    ]

    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('规则列表')
    const rows = wrapper.findAll('[data-testid="detail-rules-list"] .simple-list__row')
    expect(rows.length).toBe(3)

    expect(rows[0].text()).toContain('HTTP')
    expect(rows[0].text()).toContain('https://a.example.com')
    expect(rows[0].text()).toContain('http://10.0.0.1:8080')
    expect(rows[0].text()).toContain('web')

    expect(rows[1].text()).toContain('禁用')

    expect(rows[2].text()).toContain('L4')
    expect(rows[2].text()).toContain('tcp://0.0.0.0:25565')
    expect(rows[2].text()).toContain('192.168.1.20:25565')

    // Detail lists reuse BaseBadge instead of parallel local badge classes.
    const source = readFileSync(resolve(process.cwd(), 'src/pages/AgentDetailPage.vue'), 'utf8')
    expect(source).not.toContain('rule-type-badge')
    expect(source).not.toContain('rule-status-badge')
    expect(wrapper.find('.agent-detail__panel--inset').exists()).toBe(true)
  })

  it('navigates to rule edit page when a rule row is clicked', async () => {
    mockHttpRules = [{ id: 1, frontend_url: 'https://a.example.com', backends: [{ url: 'http://10.0.0.1:8080' }], enabled: true, tags: [] }]

    const wrapper = await mountPage()
    const row = wrapper.find('[data-testid="detail-rules-list"] .simple-list__row')
    await row.trigger('click')
    await nextTick()

    expect(routerPush).toHaveBeenCalledWith(expect.objectContaining({
      path: '/rules',
      query: expect.objectContaining({ agentId: 'edge-1', search: '#id=1' })
    }))
  })

  it('navigates to certificate and listener pages when detail rows are clicked', async () => {
    mockCertificates = [{ id: 11, domain: 'cdn.example.com', name: 'edge-cert', status: 'active', enabled: true, tags: [] }]
    mockRelayListeners = [{
      id: 21,
      name: 'public-relay',
      listen_host: '0.0.0.0',
      listen_port: 8443,
      public_host: 'relay.example.com',
      public_port: 8443,
      transport_mode: 'quic',
      tags: [],
      enabled: true,
    }]

    const wrapper = await mountPage()
    await expandSection(wrapper, '证书列表')
    await wrapper.find('[data-testid="detail-certificates-list"] .simple-list__row').trigger('click')
    await nextTick()
    expect(routerPush).toHaveBeenCalledWith(expect.objectContaining({
      path: '/certs',
      query: expect.objectContaining({ agentId: 'edge-1', search: '#id=11' })
    }))

    await expandSection(wrapper, '监听列表')
    await wrapper.find('[data-testid="detail-listeners-list"] .simple-list__row').trigger('click')
    await nextTick()
    expect(routerPush).toHaveBeenCalledWith(expect.objectContaining({
      path: '/relay-listeners',
      query: expect.objectContaining({ agentId: 'edge-1', search: '#id=21' })
    }))
  })

  it('renders certificates and relay listeners sections', async () => {
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('证书列表')
    expect(wrapper.text()).toContain('监听列表')
  })

  it('renders certificate rows with domain primary id and localized status', async () => {
    mockCertificates = [
      {
        id: 11,
        domain: 'cdn.example.com',
        name: 'edge-cert',
        status: 'active',
        enabled: true,
        tags: ['edge', 'cdn'],
        last_issue_at: '2026-06-01T08:30:00Z',
      },
      { id: 12, domain: 'fail.example.com', name: 'fail-cert', status: 'error', enabled: true, tags: [] },
    ]
    const wrapper = await mountPage()
    await expandSection(wrapper, '证书列表')

    const rows = wrapper.findAll('.simple-list__row')
    expect(rows.length).toBeGreaterThanOrEqual(2)
    expect(rows[0].text()).toContain('cdn.example.com')
    expect(rows[0].text()).toContain('edge-cert')
    expect(rows[0].text()).toContain('生效中')
    expect(rows[0].text()).toContain('edge')
    expect(rows[0].text()).toContain('cdn')
    expect(rows[0].text()).toContain('签发')
    expect(rows[0].text()).not.toContain('到期')
    expect(rows[1].text()).toContain('fail.example.com')
    expect(rows[1].text()).toContain('签发失败')
  })

  it('renders relay listener rows with primary name, secondary address, and enabled status', async () => {
    mockRelayListeners = [
      {
        id: 21,
        name: 'public-relay',
        listen_host: '0.0.0.0',
        listen_port: 8443,
        public_host: 'relay.example.com',
        public_port: 8443,
        transport_mode: 'quic',
        tags: ['public', 'edge'],
        enabled: true,
      },
      {
        id: 22,
        name: 'offline-relay',
        listen_host: '127.0.0.1',
        listen_port: 9000,
        tags: [],
        enabled: false,
      },
    ]
    const wrapper = await mountPage()
    await expandSection(wrapper, '监听列表')

    const rows = wrapper.findAll('.simple-list__row')
    expect(rows.length).toBeGreaterThanOrEqual(2)
    expect(rows[0].text()).toContain('public-relay')
    expect(rows[0].text()).toContain('relay.example.com:8443')
    expect(rows[0].text()).toContain('public')
    expect(rows[0].text()).toContain('edge')
    expect(rows[0].text()).toContain('QUIC')
    expect(rows[0].text()).toContain('启用')
    expect(rows[1].text()).toContain('offline-relay')
    expect(rows[1].text()).toContain('127.0.0.1:9000')
    expect(rows[1].text()).toContain('禁用')
  })

  it('keeps certificate and listener empty states when lists are empty', async () => {
    mockCertificates = []
    mockRelayListeners = []
    const wrapper = await mountPage()
    await expandSection(wrapper, '证书列表')
    expect(wrapper.text()).toContain('该节点暂无证书')
    await expandSection(wrapper, '监听列表')
    expect(wrapper.text()).toContain('该节点暂无监听')
  })

  it('renders system info and sync events sections', async () => {
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('系统信息')
    expect(wrapper.text()).toContain('同步事件')
  })

  it('expands sync events section by default when last apply failed', async () => {
    agentRecord.last_apply_status = 'failed'
    agentRecord.last_apply_message = 'nginx config test failed'
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('nginx config test failed')
  })

  it('count stat cards link to list pages with agentId filter', async () => {
    mockHttpRules = [{ id: 1, frontend_url: 'https://a.example.com', backends: [{ url: 'http://10.0.0.1:8080' }], enabled: true, tags: [] }]
    const wrapper = await mountPage()

    const httpCard = wrapper.findAllComponents(StatCard).find((c) => c.props('label') === 'HTTP 规则')
    expect(httpCard).toBeDefined()
    expect(httpCard.props('to')).toEqual(expect.objectContaining({ path: '/rules', query: { agentId: 'edge-1' } }))
  })

  it('renders traffic section when traffic stats are enabled', async () => {
    const wrapper = await mountPage()
    // Outer traffic section is present but collapsed by default; expand to see health overview.
    expect(wrapper.text()).toContain('流量统计')
    expect(wrapper.find('.traffic-card--health').exists()).toBe(false)

    await expandSection(wrapper, '流量统计')
    expect(wrapper.text()).toContain('健康概览')
    expect(wrapper.find('.traffic-card--health').exists()).toBe(true)
    expect(wrapper.find('.traffic-summary-cards').exists()).toBe(true)
    expect(wrapper.text()).toContain('剩余')
    expect(wrapper.text()).toContain('分析')
    expect(wrapper.text()).toContain('管理')
    expect(apiCalls.fetchTrafficPolicy).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficTrend).toHaveBeenCalledWith('edge-1', expect.objectContaining({ granularity: 'day' }))
  })

  it('hides traffic section when traffic stats are disabled', async () => {
    systemInfo = { traffic_stats_enabled: false, master_register_token: 'test-token' }
    const wrapper = await mountPage()
    const headers = wrapper.findAll('.collapsible-section__header')
    const titles = headers.map((h) => h.text())
    expect(titles).not.toContain('流量统计')
    expect(apiCalls.fetchTrafficPolicy).not.toHaveBeenCalled()
    expect(apiCalls.fetchTrafficSummary).not.toHaveBeenCalled()
    expect(apiCalls.fetchTrafficTrend).not.toHaveBeenCalled()
  })

  it('keeps health overview visible and embeds analysis/management into total/remaining modals', async () => {
    const wrapper = await mountPage()
    await expandSection(wrapper, '流量统计')

    // After expanding outer traffic, health overview is on without opening scenario modals.
    const healthCard = wrapper.find('.traffic-card--health')
    expect(healthCard.exists()).toBe(true)
    expect(healthCard.find('.traffic-section-card__title').text()).toBe('健康概览')
    expect(wrapper.find('.traffic-summary-cards').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-summary-remaining"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-health-badge"]').text()).toContain('正常')
    expect(wrapper.find('[data-testid="traffic-summary-loading"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="traffic-trend-empty"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="apexchart"]').exists()).toBe(true)

    // Independent analysis/management collapsibles are gone; scenario entries live on KPIs.
    expect(wrapper.findAll('.traffic-secondary').length).toBe(0)
    expect(wrapper.find('[data-testid="traffic-summary-open-analysis"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-summary-open-management"]').exists()).toBe(true)
    expect(wrapper.find('.traffic-breakdown__tab').exists()).toBe(false)
    expect(wrapper.findAll('button').some((button) => button.text() === '清理过期数据')).toBe(false)
  })

  it('shows blocked health badge when summary.blocked is true', async () => {
    apiCalls.fetchTrafficSummary.mockResolvedValue({
      used_bytes: 300,
      monthly_quota_bytes: 1099511627776,
      remaining_bytes: 0,
      blocked: true,
      http_rules: [],
      l4_rules: [],
      relay_listeners: []
    })
    const wrapper = await mountPage()
    await expandSection(wrapper, '流量统计')
    const badge = wrapper.find('[data-testid="traffic-health-badge"]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toContain('已阻断')
    expect(badge.classes().join(' ')).toContain('base-badge--danger')
    expect(wrapper.find('.traffic-card--health-blocked').exists()).toBe(true)
  })

  it('does not show false 无限制 while traffic summary is still loading', async () => {
    let resolveSummary
    apiCalls.fetchTrafficSummary.mockImplementation(
      () => new Promise((resolve) => {
        resolveSummary = resolve
      })
    )
    const wrapper = await mountPage()
    await expandSection(wrapper, '流量统计')

    expect(wrapper.find('[data-testid="traffic-summary-loading"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-health-badge"]').text()).toContain('加载中')
    expect(wrapper.find('.traffic-card--health').text()).not.toContain('无限制')

    resolveSummary({
      used_bytes: 300,
      monthly_quota_bytes: null,
      remaining_bytes: null,
      blocked: false,
      http_rules: [],
      l4_rules: [],
      relay_listeners: []
    })
    await nextTick()
    await vi.dynamicImportSettled()
    await nextTick()

    expect(wrapper.find('[data-testid="traffic-summary-loading"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="traffic-summary-remaining"]').text()).toBe('无限制')
    expect(wrapper.find('[data-testid="traffic-health-badge"]').text()).toContain('正常')
  })

  it('shows trend empty state when points are empty after load', async () => {
    apiCalls.fetchTrafficTrend.mockResolvedValue([])
    const wrapper = await mountPage()
    await expandSection(wrapper, '流量统计')
    expect(wrapper.find('[data-testid="traffic-trend-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-trend-empty"]').text()).toContain('暂无数据')
    expect(wrapper.find('[data-testid="apexchart"]').exists()).toBe(false)
  })

  it('opens total-traffic analysis modal with breakdown composition', async () => {
    const wrapper = await mountPage()
    await expandSection(wrapper, '流量统计')
    await wrapper.get('[data-testid="traffic-summary-open-analysis"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-testid="traffic-analysis-modal-body"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('总流量分析')
    expect(wrapper.find('[data-testid="traffic-analysis-context"]').text()).toContain('当前总流量')
    expect(wrapper.text()).toContain('HTTP 规则 #7')
    expect(wrapper.text()).toContain('12.0 KiB')

    const l4Tab = wrapper.findAll('.traffic-breakdown__tab').find((b) => b.text().includes('L4'))
    await l4Tab?.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('L4 规则 #9')
    expect(wrapper.text()).toContain('48.0 KiB')
  })

  it('does not cleanup traffic history when confirmation is cancelled', async () => {
    const wrapper = await mountPage()
    await expandSection(wrapper, '流量统计')
    await wrapper.get('[data-testid="traffic-summary-open-management"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-testid="traffic-management-modal-body"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('剩余/额度管理')
    expect(wrapper.find('[data-testid="traffic-policy-card-quota"]').exists()).toBe(true)

    await wrapper.findAll('button').find((button) => button.text() === '清理过期数据').trigger('click')
    await nextTick()
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)

    await wrapper.find('.delete-dialog-cancel').trigger('click')
    await nextTick()

    expect(apiCalls.cleanupTraffic).not.toHaveBeenCalled()
  })

  it('cleans up traffic history after confirmation', async () => {
    const wrapper = await mountPage()
    await expandSection(wrapper, '流量统计')
    await wrapper.get('[data-testid="traffic-summary-open-management"]').trigger('click')
    await nextTick()

    await wrapper.findAll('button').find((button) => button.text() === '清理过期数据').trigger('click')
    await nextTick()

    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await nextTick()

    expect(apiCalls.cleanupTraffic).toHaveBeenCalledWith('edge-1')
  })

  it('calibrates traffic current usage to zero after confirmation', async () => {
    const wrapper = await mountPage()
    await expandSection(wrapper, '流量统计')
    await wrapper.get('[data-testid="traffic-summary-open-management"]').trigger('click')
    await nextTick()

    await wrapper.findAll('button').find((button) => button.text() === '从现在归零').trigger('click')
    await nextTick()
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)

    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await nextTick()

    expect(apiCalls.calibrateTraffic).toHaveBeenCalledWith('edge-1', { used_bytes: 0 })
  })
})
