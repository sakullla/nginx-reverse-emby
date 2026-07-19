import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'

let routeQuery
let selectedAgentId
let systemInfo
let agentsData
let rulesData

const apiCalls = {
  fetchTrafficSummary: vi.fn(),
  diagnoseRule: vi.fn()
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

function listPageResult(items) {
  const list = Array.isArray(items) ? items : []
  return {
    data: {
      value: {
        items: list,
        total: list.length,
        page: 1,
        page_size: 20,
      },
    },
    isLoading: ref(false),
  }
}

vi.mock('../hooks/useRules', () => ({
  useRules: () => ({ data: { value: rulesData }, isLoading: ref(false) }),
  useRulesList: () => listPageResult(rulesData),
  useCreateRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useUpdateRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useDeleteRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() })
}))

vi.mock('../components/base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: ['modelValue', 'title', 'size', 'closeOnClickModal'],
    emits: ['update:modelValue'],
    template: '<div class="base-modal-stub"><slot /></div>'
  }
}))

vi.mock('../hooks/useDiagnostics', () => ({
  useDiagnosticTask: () => ({ data: { value: null } }),
  useDiagnoseRule: () => ({ mutateAsync: apiCalls.diagnoseRule }),
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

const diagnosticModalStub = {
  name: 'RuleDiagnosticModal',
  props: ['modelValue', 'task', 'kind'],
  template: '<div data-testid="diagnostic-modal" :data-kind="kind" :data-task-id="task?.id || \'\'">{{ task?.state || \'empty\' }}</div>'
}

async function mountPage(component) {
  const wrapper = mount(component, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient: createQueryClient() }]],
      stubs: {
        AgentPicker: true,
        ResourceListFilterBar: true,
        DeleteConfirmDialog: true,
        RuleForm: true,
        RuleDiagnosticModal: diagnosticModalStub,
        RouterLink: true
      }
    }
  })
  await nextTick()
  await vi.dynamicImportSettled()
  await nextTick()
  return wrapper
}

async function openRuleDiagnostic(wrapper) {
  document.body
    .querySelectorAll('[data-testid="base-action-menu-panel"]')
    .forEach((panel) => {
      panel.style.display = 'none'
      panel.setAttribute('aria-hidden', 'true')
    })

  await wrapper.get('button[aria-label="更多操作"]').trigger('click')
  await nextTick()

  const panel = document.body.querySelector('[role="menu"][aria-hidden="false"]')
  const item = Array.from(panel?.querySelectorAll('[role="menuitem"]') || [])
    .find((candidate) => candidate.textContent.trim() === '诊断')
  expect(item).toBeTruthy()
  item.click()
  await flushPromises()
  await nextTick()
}

async function expectTrafficUsageDisabled(component) {
  systemInfo = { traffic_stats_enabled: false }

  const wrapper = await mountPage(component)

  expect(apiCalls.fetchTrafficSummary).not.toHaveBeenCalled()
  expect(wrapper.text()).not.toContain('用量')
  expect(wrapper.text()).not.toContain('0 B')
  expect(wrapper.text()).not.toContain('0.00')
}

beforeEach(() => {
  routeQuery = { agentId: 'edge-1' }
  selectedAgentId = 'edge-1'
  systemInfo = { traffic_stats_enabled: true }
  agentsData = [{ id: 'edge-1', name: 'edge-1', desired_revision: 1, current_revision: 1, last_apply_status: 'success' }]
  rulesData = [{ id: 7, frontend_url: 'https://app.example.test', backends: [{ url: 'http://origin.example.test' }], enabled: true }]
  vi.clearAllMocks()
  apiCalls.fetchTrafficSummary.mockResolvedValue({
    http_rules: [{ scope_type: 'http_rule', scope_id: '7', rx_bytes: 1024, tx_bytes: 2048, accounted_bytes: 3072 }],
    l4_rules: [{ scope_type: 'l4_rule', scope_id: '9', rx_bytes: 4096, tx_bytes: 8192, accounted_bytes: 12288 }],
    relay_listeners: [{ scope_type: 'relay_listener', scope_id: '11', rx_bytes: 16384, tx_bytes: 32768, accounted_bytes: 49152 }]
  })
  apiCalls.diagnoseRule.mockResolvedValue({
    ok: true,
    task_id: 'task-http-1',
    task: { id: 'task-http-1', state: 'pending' }
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

  it('passes the returned HTTP diagnosis task to the modal immediately', async () => {
    const { default: RulesPage } = await import('./RulesPage.vue')
    const wrapper = await mountPage(RulesPage)

    await openRuleDiagnostic(wrapper)

    expect(apiCalls.diagnoseRule).toHaveBeenCalled()
    expect(wrapper.get('[data-testid="diagnostic-modal"]').attributes('data-task-id')).toBe('task-http-1')
    expect(wrapper.get('[data-testid="diagnostic-modal"]').text()).toContain('pending')
  })

  it('hides traffic and skips summary requests when traffic stats are disabled', async () => {
    const { default: RulesPage } = await import('./RulesPage.vue')

    await expectTrafficUsageDisabled(RulesPage)
  })

  it('loads per-agent traffic summary under all-agents filter using item agent_id', async () => {
    const { ALL_AGENTS_FILTER } = await import('../utils/agentFilter.js')
    routeQuery = { agentId: ALL_AGENTS_FILTER }
    selectedAgentId = ALL_AGENTS_FILTER
    agentsData = [
      { id: 'edge-1', name: 'edge-1', desired_revision: 1, current_revision: 1, last_apply_status: 'success' },
      { id: 'edge-2', name: 'edge-2', desired_revision: 1, current_revision: 1, last_apply_status: 'success' },
    ]
    rulesData = [
      {
        id: 7,
        agent_id: 'edge-1',
        frontend_url: 'https://app.example.test',
        backends: [{ url: 'http://origin.example.test' }],
        enabled: true,
      },
      {
        id: 8,
        agent_id: 'edge-2',
        frontend_url: 'https://other.example.test',
        backends: [{ url: 'http://origin2.example.test' }],
        enabled: true,
      },
    ]
    apiCalls.fetchTrafficSummary.mockImplementation(async (id) => {
      if (id === 'edge-1') {
        return {
          used_bytes: 4096,
          http_rules: [{ scope_type: 'http_rule', scope_id: '7', rx_bytes: 1024, tx_bytes: 2048, accounted_bytes: 3072 }],
        }
      }
      if (id === 'edge-2') {
        return {
          used_bytes: 8192,
          http_rules: [{ scope_type: 'http_rule', scope_id: '8', rx_bytes: 512, tx_bytes: 512, accounted_bytes: 1024 }],
        }
      }
      return { http_rules: [] }
    })

    const { default: RulesPage } = await import('./RulesPage.vue')
    const wrapper = await mountPage(RulesPage)

    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-1')
    expect(apiCalls.fetchTrafficSummary).toHaveBeenCalledWith('edge-2')
    expect(wrapper.text()).toContain('用量 3.00 KiB')
    expect(wrapper.text()).toContain('用量 1.00 KiB')
  })
})
