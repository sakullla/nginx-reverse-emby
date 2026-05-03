import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'

let routeQuery
let selectedAgentId
let systemInfo
let agentsData
let rulesData
let l4RulesData
let relayListenersData

const apiCalls = {
  fetchTrafficSummary: vi.fn()
}

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: routeQuery }),
  useRouter: () => ({ replace: vi.fn() }),
  RouterLink: {
    props: ['to'],
    template: '<a><slot /></a>'
  }
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({
    selectedAgentId: { value: selectedAgentId },
    systemInfo: { value: systemInfo }
  })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: { value: agentsData } })
}))

vi.mock('../hooks/useRules', () => ({
  useRules: () => ({ data: { value: rulesData }, isLoading: { value: false } }),
  useCreateRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useUpdateRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useDeleteRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() })
}))

vi.mock('../hooks/useL4Rules', () => ({
  useL4Rules: () => ({ data: { value: l4RulesData }, isLoading: { value: false } }),
  useCreateL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useUpdateL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useDeleteL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() })
}))

vi.mock('../hooks/useRelayListeners', () => ({
  useRelayListeners: () => ({ data: { value: relayListenersData }, isLoading: { value: false } }),
  useDeleteRelayListener: () => ({ isPending: { value: false }, mutate: vi.fn() }),
  useUpdateRelayListener: () => ({ mutate: vi.fn() })
}))

vi.mock('../hooks/useDiagnostics', () => ({
  useDiagnosticTask: () => ({ data: { value: null } }),
  useDiagnoseRule: () => ({ mutateAsync: vi.fn() }),
  useDiagnoseL4Rule: () => ({ mutateAsync: vi.fn() })
}))

vi.mock('../api', () => ({
  fetchTrafficSummary: (...args) => apiCalls.fetchTrafficSummary(...args)
}))

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false }
    }
  })
}

async function mountPage(component) {
  const wrapper = mount(component, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient: createQueryClient() }]],
      stubs: {
        AgentPicker: true,
        BaseModal: true,
        DeleteConfirmDialog: true,
        RuleForm: true,
        L4RuleForm: true,
        RelayListenerForm: true,
        RuleDiagnosticModal: true,
        RouterLink: true
      }
    }
  })
  await nextTick()
  await vi.dynamicImportSettled()
  await nextTick()
  return wrapper
}

beforeEach(() => {
  routeQuery = { agentId: 'edge-1' }
  selectedAgentId = 'edge-1'
  systemInfo = { traffic_stats_enabled: true }
  agentsData = [{ id: 'edge-1', name: 'edge-1', desired_revision: 1, current_revision: 1, last_apply_status: 'success' }]
  rulesData = [{ id: 7, frontend_url: 'https://app.example.test', backend_url: 'http://origin.example.test', enabled: true }]
  l4RulesData = [{ id: 9, name: 'tcp-app', protocol: 'tcp', listen_host: '0.0.0.0', listen_port: 443, upstream_host: '10.0.0.1', upstream_port: 443, enabled: true }]
  relayListenersData = [{ id: 11, name: 'relay-main', enabled: true, public_host: 'relay.example.test', public_port: 8443, listen_host: '0.0.0.0', listen_port: 8443 }]
  vi.clearAllMocks()
  apiCalls.fetchTrafficSummary.mockResolvedValue({
    http_rules: [{ scope_type: 'http_rule', scope_id: '7', rx_bytes: 1024, tx_bytes: 2048, accounted_bytes: 3072 }],
    l4_rules: [{ scope_type: 'l4_rule', scope_id: '9', rx_bytes: 4096, tx_bytes: 8192, accounted_bytes: 12288 }],
    relay_listeners: [{ scope_type: 'relay_listener', scope_id: '11', rx_bytes: 16384, tx_bytes: 32768, accounted_bytes: 49152 }]
  })
})

describe('rule list traffic usage', () => {
  it('renders HTTP rule accounted usage from traffic summary', async () => {
    const { default: RulesPage } = await import('./RulesPage.vue')

    const wrapper = await mountPage(RulesPage)

    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-1')
    expect(wrapper.text()).toContain('用量 3.00 KiB')
    expect(wrapper.text()).toContain('入 1.00 KiB')
    expect(wrapper.text()).toContain('出 2.00 KiB')
  })

  it('renders L4 rule accounted usage from traffic summary', async () => {
    const { default: L4RulesPage } = await import('./L4RulesPage.vue')

    const wrapper = await mountPage(L4RulesPage)

    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-1')
    expect(wrapper.text()).toContain('用量 12.0 KiB')
    expect(wrapper.text()).toContain('入 4.00 KiB')
    expect(wrapper.text()).toContain('出 8.00 KiB')
  })

  it('renders relay listener accounted usage from traffic summary', async () => {
    const { default: RelayListenersPage } = await import('./RelayListenersPage.vue')

    const wrapper = await mountPage(RelayListenersPage)

    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-1')
    expect(wrapper.text()).toContain('用量 48.0 KiB')
    expect(wrapper.text()).toContain('入 16.0 KiB')
    expect(wrapper.text()).toContain('出 32.0 KiB')
  })

  it('hides rule traffic and skips summary requests when traffic stats are disabled', async () => {
    systemInfo = { traffic_stats_enabled: false }
    const { default: RulesPage } = await import('./RulesPage.vue')

    const wrapper = await mountPage(RulesPage)

    expect(apiCalls.fetchTrafficSummary).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('用量')
    expect(wrapper.text()).not.toContain('入 0 B')
  })
})
