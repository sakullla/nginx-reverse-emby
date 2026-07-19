import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import AgentDetailPage from './AgentDetailPage.vue'
import AgentStatusBadge from '../components/AgentStatusBadge.vue'
import { recordAcceptedOperation, resetOperations } from '../stores/operations'
import { messageStore } from '../stores/messages'

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
          name: 'RouterLink',
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
  resetOperations()
  localStorage.clear()
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
  apiCalls.updateAgent.mockResolvedValue({})
})

describe('AgentDetailPage', () => {
  it('restores accepted mutation status after navigation or reload', async () => {
    recordAcceptedOperation({
      operation_id: 'operation-page',
      status_url: '/panel-api/operations/operation-page',
      agent_id: 'edge-1',
      desired_revision: 2,
      apply_status: 'pending'
    })

    const wrapper = await mountPage()

    expect(wrapper.get('[aria-label="配置生效状态"]').text()).toContain('已保存，等待生效')
    expect(wrapper.text()).toContain('revision 2')
    wrapper.unmount()
  })

  it('renders the identity row per v4: name, fused status+last-seen, DDNS domain, version', async () => {
    agentRecord.status = 'online'
    agentRecord.mode = 'master'
    agentRecord.version = 'v1.2.3'
    agentRecord.tags = ['prod', 'edge']
    agentRecord.ddns_domain = 'edge.example.com'
    agentRecord.ddns_status = { status: 'ok' }

    const wrapper = await mountPage()

    // 数据断言:名字、状态徽标、最后活跃、DDNS 域名与解析徽标、版本;
    // 模式与标签不出现在身份行。
    expect(wrapper.find('[data-testid="detail-name"]').text()).toContain('边缘节点-01')
    expect(wrapper.findComponent(AgentStatusBadge).exists()).toBe(true)
    expect(wrapper.find('[data-testid="detail-header-lastseen"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="detail-ddns-domain"]').text()).toContain('edge.example.com')
    expect(wrapper.find('[data-testid="detail-ddns-status"]').text()).toContain('已解析')
    expect(wrapper.find('[data-testid="detail-header-version"]').text()).toContain('v1.2.3')
    expect(wrapper.find('[data-testid="detail-ddns-summary"]').exists()).toBe(true)
  })

  it('keeps IPv4 visible in the collapsed header using the resolved DDNS address as fallback', async () => {
    localStorage.setItem('nre.agent-detail.summary-collapsed', '1')
    agentRecord.ddns_domain = 'edge.example.com'
    agentRecord.ddns_status = {
      status: 'ok',
      last_resolved_ipv4: '203.0.113.25'
    }

    const wrapper = await mountPage()

    expect(wrapper.find('[data-testid="detail-summary-body"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="detail-header-ipv4"]').text()).toContain('203.0.113.25')
  })

  it('opens the DDNS modal from the header icon button', async () => {
    const wrapper = await mountPage()
    await wrapper.find('[data-testid="detail-ddns-summary"]').trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="detail-ddns-modal-body"]').exists()).toBe(true)
  })

  it('hides DDNS domain and status in the identity row when unconfigured', async () => {
    agentRecord.ddns_domain = ''
    const wrapper = await mountPage()

    // 未配置时身份行不显示域名/解析徽标;右上角配置入口仍在。
    expect(wrapper.find('[data-testid="detail-ddns-domain"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="detail-ddns-status"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="detail-ddns-summary"]').exists()).toBe(true)
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

  it('renders the rules section collapsed by default and lists HTTP/L4 rules when expanded', async () => {
    mockHttpRules = [
      { id: 1, frontend_url: 'https://a.example.com', backends: [{ url: 'http://10.0.0.1:8080' }], enabled: true, tags: ['web', 'prod'] },
      { id: 2, frontend_url: 'https://b.example.com', backends: [{ url: 'http://10.0.0.2:8080' }], enabled: false, tags: [] }
    ]
    mockL4Rules = [
      { id: 101, protocol: 'tcp', listen_host: '0.0.0.0', listen_port: 25565, backends: [{ host: '192.168.1.20', port: 25565 }], enabled: true, tags: ['game'] }
    ]

    const wrapper = await mountPage()

    expect(wrapper.text()).toContain('规则列表')
    // 默认折叠:行不可见,展开后可见。
    expect(wrapper.find('[data-testid="detail-rules-list"] .simple-list__row').exists()).toBe(false)

    await expandSection(wrapper, '规则列表')
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
  })

  it('navigates to rule edit page when a rule row is clicked', async () => {
    mockHttpRules = [{ id: 1, frontend_url: 'https://a.example.com', backends: [{ url: 'http://10.0.0.1:8080' }], enabled: true, tags: [] }]

    const wrapper = await mountPage()
    await expandSection(wrapper, '规则列表')
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

  it('limits long rule lists to 10 rows with an expand-all / collapse entry', async () => {
    mockHttpRules = Array.from({ length: 12 }, (_, i) => ({
      id: i + 1,
      frontend_url: `https://r${i + 1}.example.com`,
      backends: [{ url: 'http://10.0.0.1:8080' }],
      enabled: true,
      tags: []
    }))

    const wrapper = await mountPage()
    await expandSection(wrapper, '规则列表')

    expect(wrapper.findAll('[data-testid="detail-rules-list"] .simple-list__row').length).toBe(10)
    const more = wrapper.find('[data-testid="detail-rules-more"]')
    expect(more.exists()).toBe(true)
    expect(more.text()).toContain('查看全部 12 条')

    await more.trigger('click')
    await nextTick()
    expect(wrapper.findAll('[data-testid="detail-rules-list"] .simple-list__row').length).toBe(12)
    expect(wrapper.find('[data-testid="detail-rules-more"]').text()).toContain('收起')

    await wrapper.find('[data-testid="detail-rules-more"]').trigger('click')
    await nextTick()
    expect(wrapper.findAll('[data-testid="detail-rules-list"] .simple-list__row').length).toBe(10)
  })

  it('expands sync events section by default when last apply failed', async () => {
    agentRecord.last_apply_status = 'failed'
    agentRecord.last_apply_message = 'nginx config test failed'
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('nginx config test failed')
  })

  it('embeds health overview and trend chart in the summary card when traffic stats are enabled', async () => {
    const wrapper = await mountPage()

    // No standalone traffic collapsible section; original components live in
    // the summary card body, visible without expanding anything.
    const headers = wrapper.findAll('.collapsible-section__header')
    expect(headers.map((h) => h.text())).not.toContain('流量统计')

    const body = wrapper.find('[data-testid="detail-summary-body"]')
    expect(body.find('[data-testid="detail-traffic-health"]').exists()).toBe(true)
    expect(body.find('.traffic-summary-cards').exists()).toBe(true)
    expect(body.text()).toContain('剩余')
    expect(body.text()).toContain('分析')
    expect(body.text()).toContain('管理')
    expect(body.find('[data-testid="detail-traffic-trend"]').exists()).toBe(true)
    expect(body.find('[data-testid="apexchart"]').exists()).toBe(true)
    expect(apiCalls.fetchTrafficPolicy).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficTrend).toHaveBeenCalledWith('edge-1', expect.objectContaining({ granularity: 'day' }))
  })

  it('hides traffic content when traffic stats are disabled', async () => {
    systemInfo = { traffic_stats_enabled: false, master_register_token: 'test-token' }
    const wrapper = await mountPage()

    const headers = wrapper.findAll('.collapsible-section__header')
    expect(headers.map((h) => h.text())).not.toContain('流量统计')
    expect(wrapper.find('[data-testid="detail-traffic-health"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="detail-traffic-trend"]').exists()).toBe(false)
    expect(wrapper.find('.traffic-summary-cards').exists()).toBe(false)
    expect(apiCalls.fetchTrafficPolicy).not.toHaveBeenCalled()
    expect(apiCalls.fetchTrafficSummary).not.toHaveBeenCalled()
    expect(apiCalls.fetchTrafficTrend).not.toHaveBeenCalled()
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
    const badge = wrapper.find('[data-testid="traffic-health-badge"]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toContain('已阻断')
    expect(badge.classes().join(' ')).toContain('base-badge--danger')
  })

  it('does not show false 无限制 while traffic summary is still loading', async () => {
    let resolveSummary
    apiCalls.fetchTrafficSummary.mockImplementation(
      () => new Promise((resolve) => {
        resolveSummary = resolve
      })
    )
    const wrapper = await mountPage()

    expect(wrapper.find('[data-testid="traffic-summary-loading"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-health-badge"]').text()).toContain('加载中')
    expect(wrapper.find('[data-testid="detail-traffic-health"]').text()).not.toContain('无限制')

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
    expect(wrapper.find('[data-testid="traffic-trend-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-trend-empty"]').text()).toContain('暂无数据')
    expect(wrapper.find('[data-testid="apexchart"]').exists()).toBe(false)
  })

  it('opens total-traffic analysis modal with breakdown composition', async () => {
    const wrapper = await mountPage()
    await wrapper.get('[data-testid="traffic-summary-open-analysis"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-testid="traffic-analysis-modal-body"]').exists()).toBe(true)
    expect(wrapper.find('.traffic-scenario-modal--analysis').exists()).toBe(true)
    expect(wrapper.text()).toContain('总流量分析')
    expect(wrapper.find('[data-testid="traffic-analysis-context"]').text()).toContain('当前总流量')
    expect(wrapper.find('[data-testid="traffic-analysis-section-breakdown"]').exists()).toBe(true)
    expect(wrapper.find('.traffic-scenario-modal__section-title').text()).toContain('分项构成')
    expect(wrapper.find('.traffic-scenario-modal__panel--table').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-analysis-breakdown-panel"]').exists()).toBe(true)
    expect(wrapper.find('.traffic-breakdown__th--usage').exists()).toBe(true)
    expect(wrapper.find('.traffic-breakdown__share-bar').exists()).toBe(true)
    expect(wrapper.find('.traffic-breakdown__th--io').text()).toContain('收发')
    expect(wrapper.text()).toContain('HTTP 规则 #7')
    expect(wrapper.text()).toContain('12.0 KiB')

    const l4Tab = wrapper.findAll('.traffic-breakdown__tab').find((b) => b.text().includes('L4'))
    await l4Tab?.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('L4 规则 #9')
    expect(wrapper.text()).toContain('48.0 KiB')
    expect(wrapper.find('.traffic-breakdown__share-bar').exists()).toBe(true)
  })

  it('cleans up traffic history after confirmation', async () => {
    const wrapper = await mountPage()
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
    await wrapper.get('[data-testid="traffic-summary-open-management"]').trigger('click')
    await nextTick()

    await wrapper.findAll('button').find((button) => button.text() === '从现在归零').trigger('click')
    await nextTick()
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)

    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await nextTick()

    expect(apiCalls.calibrateTraffic).toHaveBeenCalledWith('edge-1', { used_bytes: 0 })
  })

  it('collapses the summary card body and persists the preference globally', async () => {
    const wrapper = await mountPage()
    expect(wrapper.find('[data-testid="detail-summary-body"]').exists()).toBe(true)

    await wrapper.find('[data-testid="detail-action-collapse"]').trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="detail-summary-body"]').exists()).toBe(false)
    // Header actions stay usable while collapsed; the DDNS capsule button lives
    // in the header and stays reachable.
    expect(wrapper.find('[data-testid="detail-ddns-summary"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="detail-action-delete"]').exists()).toBe(true)
    expect(localStorage.getItem('nre.agent-detail.summary-collapsed')).toBe('1')
    wrapper.unmount()

    // A fresh mount (e.g. another node's detail page) starts collapsed from the
    // stored global preference.
    const remounted = await mountPage()
    expect(remounted.find('[data-testid="detail-summary-body"]').exists()).toBe(false)

    await remounted.find('[data-testid="detail-action-collapse"]').trigger('click')
    await nextTick()
    expect(remounted.find('[data-testid="detail-summary-body"]').exists()).toBe(true)
    expect(localStorage.getItem('nre.agent-detail.summary-collapsed')).toBe('0')
  })

  it('shows IPv4/IPv6/domain/status in the system info identity card', async () => {
    agentRecord.last_seen_ipv4 = '203.0.113.10'
    agentRecord.last_seen_ipv6 = '2001:db8::10'
    agentRecord.ddns_domain = 'edge.example.com'
    agentRecord.ddns_status = { status: 'error' }
    const wrapper = await mountPage()
    await expandSection(wrapper, '系统信息')

    expect(wrapper.find('[data-testid="detail-identity-ipv4"]').text()).toContain('203.0.113.10')
    expect(wrapper.find('[data-testid="detail-identity-ipv6"]').text()).toContain('2001:db8::10')
    expect(wrapper.find('[data-testid="detail-identity-domain"]').text()).toContain('edge.example.com')
    expect(wrapper.find('[data-testid="detail-identity-ddns-status"]').text()).toContain('解析失败')
  })

  it('opens the DDNS config modal seeded from ddns_config and round-trips the family state', async () => {
    agentRecord.ddns_config = {
      enabled: true,
      domain: 'edge.example.com',
      ipv4: { enabled: true, source: 'public_api' },
      ipv6: { enabled: true, source: 'interface', interface: 'eth0' }
    }
    const wrapper = await mountPage()

    await wrapper.find('[data-testid="detail-ddns-summary"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-testid="detail-ddns-modal-body"]').exists()).toBe(true)
    // The form must seed the full contract ddns_config (the read path the backend
    // now exposes), not just the domain — the interface input renders only when
    // source==='interface', so its value proves family state was seeded.
    expect(wrapper.find('[data-testid="agent-ddns-form-enabled"]').element.checked).toBe(true)
    expect(wrapper.find('[data-testid="agent-ddns-form-domain"]').element.value).toBe('edge.example.com')
    expect(wrapper.find('[data-testid="agent-ddns-form-ipv6-interface"]').element.value).toBe('eth0')

    await wrapper.find('[data-testid="agent-ddns-form-save"]').trigger('click')
    await nextTick()

    expect(apiCalls.updateAgent).toHaveBeenCalledWith({
      agentId: 'edge-1',
      payload: { ddns_config: expect.objectContaining({
        enabled: true,
        domain: 'edge.example.com',
        ipv6: expect.objectContaining({ enabled: true, source: 'interface', interface: 'eth0' })
      }) }
    })
  })

  it('derives the master switch from family state for legacy configs without enabled', async () => {
    agentRecord.ddns_config = {
      domain: 'edge.example.com',
      ipv4: { enabled: true, source: 'public_api' }
    }
    const wrapper = await mountPage()

    await wrapper.find('[data-testid="detail-ddns-summary"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-testid="agent-ddns-form-enabled"]').element.checked).toBe(true)
  })

  it('saves the switch state with success feedback', async () => {
    const successSpy = vi.spyOn(messageStore, 'success')
    agentRecord.ddns_config = {
      enabled: false,
      domain: 'edge.example.com',
      ipv4: { enabled: true, source: 'public_api' }
    }
    const wrapper = await mountPage()

    await wrapper.find('[data-testid="detail-ddns-summary"]').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="agent-ddns-form-save"]').trigger('click')
    await nextTick()

    // Switch-off save preserves the sub-config verbatim and confirms the save.
    expect(apiCalls.updateAgent).toHaveBeenCalledWith({
      agentId: 'edge-1',
      payload: { ddns_config: expect.objectContaining({
        enabled: false,
        domain: 'edge.example.com',
        ipv4: expect.objectContaining({ enabled: true, source: 'public_api' })
      }) }
    })
    expect(successSpy).toHaveBeenCalled()
  })

})
