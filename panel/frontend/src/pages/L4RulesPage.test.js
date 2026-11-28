import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick, reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import L4RulesPage from './L4RulesPage.vue'
import { updateL4Rule, deleteL4Rule } from '../api'
import { messageStore } from '../stores/messages'

let route
let routerReplace
let selectedAgentId
let agentsData
let rulesData
let listState
let diagnoseMutateAsync
const mountedWrappers = []
const queryClients = []

vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ replace: routerReplace }),
  RouterLink: { props: ['to'], template: '<a><slot /></a>' }
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({
    selectedAgentId: { value: selectedAgentId },
    systemInfo: { value: { traffic_stats_enabled: false } }
  })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: { value: agentsData } })
}))

vi.mock('../hooks/useL4Rules', () => ({
  useL4RulesList: () => listState,
  useCreateL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useUpdateL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useDeleteL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: ref(false) })
}))

vi.mock('../hooks/useDiagnostics', () => ({
  useDiagnoseL4Rule: () => ({ mutateAsync: (...args) => diagnoseMutateAsync(...args) }),
  useDiagnosticTask: () => ({ data: ref(null) })
}))

vi.mock('../hooks/useTrafficSummaryForResources', () => ({
  useTrafficSummaryForResources: () => ({ nodeTotalFor: () => 0, trafficFor: () => null })
}))

vi.mock('../api', () => ({
  fetchL4Rules: vi.fn().mockResolvedValue([]),
  fetchAllAgentsL4Rules: vi.fn().mockResolvedValue([]),
  fetchRelayListeners: vi.fn().mockResolvedValue([]),
  fetchAllAgentsRelayListeners: vi.fn().mockResolvedValue([]),
  fetchEgressProfiles: vi.fn().mockResolvedValue({ profiles: [] }),
  updateL4Rule: vi.fn(),
  deleteL4Rule: vi.fn()
}))

const DeleteDialogStub = {
  name: 'DeleteConfirmDialog',
  props: ['show', 'title', 'message', 'name', 'error', 'loading', 'confirmText'],
  emits: ['confirm', 'cancel'],
  template: `<div v-if="show" class="delete-dialog-stub" :data-title="title">
    <span class="delete-dialog-stub__message">{{ message }}</span>
    <span class="delete-dialog-stub__error">{{ error }}</span>
    <button class="delete-dialog-stub__confirm" @click="$emit('confirm')">confirm</button>
  </div>`
}

function makeListState(items, overrides = {}) {
  const list = Array.isArray(items) ? items : []
  return {
    data: ref({ items: list, total: list.length, page: 1, page_size: 20 }),
    isLoading: ref(false),
    isError: ref(false),
    error: ref(null),
    refetch: vi.fn(),
    isRefetching: ref(false),
    ...overrides
  }
}

function mountPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  queryClients.push(queryClient)
  const wrapper = mount(L4RulesPage, {
    attachTo: document.body,
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]],
      stubs: {
        RouterLink: { props: ['to'], template: '<a><slot /></a>' },
        AgentSearchSelect: { props: ['modelValue', 'agents'], template: '<div />' },
        L4RuleForm: true,
        BaseModal: true,
        DeleteConfirmDialog: DeleteDialogStub,
        RuleDiagnosticModal: true,
        TrafficTrendModal: true,
        IdCandidateModal: true,
        CreateAgentPicker: true,
        OperationStatusList: true,
        ListPagination: true
      }
    }
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

function cards(wrapper) {
  return wrapper.findAll('.rule-grid .base-list-card')
}

function bulkDialog(wrapper) {
  return wrapper.findAllComponents(DeleteDialogStub).find((d) => d.props('title') === '批量删除规则')
}

async function enterBulkAndSelect(wrapper, indexes) {
  await wrapper.get('[data-testid="l4-bulk-toggle"]').trigger('click')
  for (const index of indexes) {
    await cards(wrapper)[index].trigger('click')
  }
}

afterEach(() => {
  while (mountedWrappers.length) mountedWrappers.pop().unmount()
  while (queryClients.length) queryClients.pop().clear()
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

beforeEach(() => {
  route = reactive({ query: { agentId: '1' } })
  routerReplace = vi.fn((target) => {
    if (target?.query) route.query = target.query
  })
  selectedAgentId = '1'
  agentsData = [{ id: '1', name: 'master' }]
  rulesData = [
    { id: 11, agent_id: '1', protocol: 'tcp', listen_host: '0.0.0.0', listen_port: 8011, enabled: true, backends: [{ host: 'a', port: 1 }] },
    { id: 12, agent_id: '1', protocol: 'udp', listen_host: '0.0.0.0', listen_port: 8012, enabled: true, backends: [{ host: 'b', port: 2 }] },
    { id: 13, agent_id: '1', protocol: 'tcp', listen_host: '0.0.0.0', listen_port: 8013, enabled: false, backends: [{ host: 'c', port: 3 }] }
  ]
  listState = makeListState(rulesData)
  diagnoseMutateAsync = vi.fn().mockResolvedValue({ task_id: 'task-1', task: { state: 'completed' } })
  updateL4Rule.mockReset().mockResolvedValue({})
  deleteL4Rule.mockReset().mockResolvedValue({})
})

describe('L4RulesPage bulk mode', () => {
  it('enters selection mode, card clicks select instead of editing, and cancel clears', async () => {
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.find('.bulk-action-bar').exists()).toBe(false)
    await enterBulkAndSelect(wrapper, [0, 2])

    expect(wrapper.get('.bulk-action-bar').text()).toContain('已选 2 项')
    // Editor never opened by the selecting clicks.
    const modalOpen = wrapper.findAllComponents({ name: 'BaseModal' }).some((m) => m.props('modelValue') === true)
    expect(modalOpen).toBe(false)

    await wrapper.get('[data-bulk-action="cancel"]').trigger('click')
    expect(wrapper.find('.bulk-action-bar').exists()).toBe(false)

    await wrapper.get('[data-testid="l4-bulk-toggle"]').trigger('click')
    expect(wrapper.get('.bulk-action-bar').text()).toContain('已选 0 项')
  })

  it('exits selection mode with Escape', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await enterBulkAndSelect(wrapper, [0])

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(wrapper.find('.bulk-action-bar').exists()).toBe(false)
  })

  it('bulk-disables selected rules via updateL4Rule and keeps failures selected', async () => {
    const errorSpy = vi.spyOn(messageStore, 'error').mockImplementation(() => {})
    updateL4Rule.mockImplementation((agent, id) => (
      id === 13 ? Promise.reject(new Error('节点离线')) : Promise.resolve({})
    ))
    const wrapper = mountPage()
    await flushPromises()
    await enterBulkAndSelect(wrapper, [0, 2])

    await wrapper.get('[data-bulk-action="disable"]').trigger('click')
    await flushPromises()

    expect(updateL4Rule).toHaveBeenCalledTimes(2)
    expect(updateL4Rule).toHaveBeenCalledWith('1', 11, { enabled: false })
    expect(updateL4Rule).toHaveBeenCalledWith('1', 13, { enabled: false })
    expect(errorSpy).toHaveBeenCalledTimes(1)
    expect(errorSpy.mock.calls[0][0]).toContain('失败 1 项')
    expect(errorSpy.mock.calls[0][0]).toContain('节点离线')
    expect(wrapper.get('.bulk-action-bar').text()).toContain('已选 1 项')
    expect(cards(wrapper)[2].classes()).toContain('l4-card--selected')
    expect(cards(wrapper)[0].classes()).not.toContain('l4-card--selected')
  })

  it('bulk-enables with a single success summary', async () => {
    const successSpy = vi.spyOn(messageStore, 'success').mockImplementation(() => {})
    const wrapper = mountPage()
    await flushPromises()
    await enterBulkAndSelect(wrapper, [1, 2])

    await wrapper.get('[data-bulk-action="enable"]').trigger('click')
    await flushPromises()

    expect(updateL4Rule).toHaveBeenCalledWith('1', 12, { enabled: true })
    expect(updateL4Rule).toHaveBeenCalledWith('1', 13, { enabled: true })
    expect(successSpy).toHaveBeenCalledTimes(1)
    expect(successSpy.mock.calls[0][0]).toBe('已启用 2 项')
    expect(wrapper.get('.bulk-action-bar').text()).toContain('已选 0 项')
  })

  it('confirms before bulk delete and shows partial failure in the dialog', async () => {
    vi.spyOn(messageStore, 'error').mockImplementation(() => {})
    deleteL4Rule.mockImplementation((agent, id) => (
      id === 12 ? Promise.reject(new Error('busy')) : Promise.resolve({})
    ))
    const wrapper = mountPage()
    await flushPromises()
    await enterBulkAndSelect(wrapper, [0, 1])

    await wrapper.get('[data-bulk-action="delete"]').trigger('click')
    expect(deleteL4Rule).not.toHaveBeenCalled()
    const dialog = bulkDialog(wrapper)
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('message')).toContain('2 条')

    await dialog.get('.delete-dialog-stub__confirm').trigger('click')
    await flushPromises()

    expect(deleteL4Rule).toHaveBeenCalledWith('1', 11)
    expect(deleteL4Rule).toHaveBeenCalledWith('1', 12)
    expect(bulkDialog(wrapper).props('show')).toBe(true)
    expect(bulkDialog(wrapper).props('error')).toContain('busy')
    expect(wrapper.get('.bulk-action-bar').text()).toContain('已选 1 项')
  })

  it('clears the selection when filters change', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await enterBulkAndSelect(wrapper, [0, 1])
    expect(wrapper.get('.bulk-action-bar').text()).toContain('已选 2 项')

    route.query = { ...route.query, enabled: 'true' }
    await flushPromises()

    expect(wrapper.get('.bulk-action-bar').text()).toContain('已选 0 项')
  })

  it('adds bulk-active padding class, places BulkActionBar after ListPagination, and supports select-all in the bar', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('.rules-page').classes()).not.toContain('rules-page--bulk-active')

    await wrapper.get('[data-testid="l4-bulk-toggle"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('.rules-page').classes()).toContain('rules-page--bulk-active')

    const children = Array.from(wrapper.get('.rules-page').element.children)
    const paginationEl = wrapper.findComponent({ name: 'ListPagination' }).element
    const bulkBarEl = wrapper.findComponent({ name: 'BulkActionBar' }).element
    expect(children.indexOf(bulkBarEl)).toBeGreaterThan(children.indexOf(paginationEl))

    const selectAllBtn = wrapper.get('[data-bulk-action="select-all"]')
    expect(selectAllBtn.text()).toBe('全选本页')
    await selectAllBtn.trigger('click')
    await flushPromises()
    expect(wrapper.get('.bulk-action-bar__count').text()).toBe('已选 3 项')
    expect(wrapper.get('[data-bulk-action="select-all"]').text()).toBe('取消全选')

    await wrapper.get('[data-bulk-action="select-all"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('.bulk-action-bar__count').text()).toBe('已选 0 项')
    expect(wrapper.get('[data-bulk-action="select-all"]').text()).toBe('全选本页')

    await wrapper.get('[data-bulk-action="cancel"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('.rules-page').classes()).not.toContain('rules-page--bulk-active')
  })

  it('drops selected keys that disappear after a background refetch', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="l4-bulk-toggle"]').trigger('click')
    await wrapper.get('[data-bulk-action="select-all"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('.bulk-action-bar__count').text()).toBe('已选 3 项')

    const remaining = rulesData.filter((rule) => rule.id !== 12)
    listState.data.value = { items: remaining, total: remaining.length, page: 1, page_size: 20 }
    await flushPromises()
    expect(wrapper.get('.bulk-action-bar__count').text()).toBe('已选 2 项')
  })

  it('supports retrying L4 diagnostics from inside RuleDiagnosticModal', async () => {
    diagnoseMutateAsync
      .mockResolvedValueOnce({ task_id: 'task-1', task: { state: 'completed' } })
      .mockResolvedValueOnce({ task_id: 'task-2', task: { state: 'completed' } })

    const wrapper = mountPage()
    await flushPromises()

    wrapper.findAllComponents({ name: 'L4RuleItem' })[0].vm.$emit('diagnose', rulesData[0])
    await flushPromises()
    expect(diagnoseMutateAsync).toHaveBeenCalledTimes(1)
    expect(diagnoseMutateAsync).toHaveBeenLastCalledWith({ id: 11, agentId: '1' })

    const modal = wrapper.findComponent({ name: 'RuleDiagnosticModal' })
    expect(modal.props('modelValue')).toBe(true)
    expect(modal.props('canRetry')).toBe(true)
    expect(modal.props('agentLabel')).toBe('master')
    expect(modal.props('ruleLabel')).toBe('0.0.0.0:8011')

    modal.vm.$emit('retry')
    await flushPromises()
    expect(diagnoseMutateAsync).toHaveBeenCalledTimes(2)
    expect(diagnoseMutateAsync).toHaveBeenLastCalledWith({ id: 11, agentId: '1' })
  })

  it('keeps RuleDiagnosticModal open with failed task state on retry failure without duplicate toast and allows retrying again', async () => {
    const errorSpy = vi.spyOn(messageStore, 'error').mockImplementation(() => {})
    diagnoseMutateAsync
      .mockResolvedValueOnce({ task_id: 'task-1', task: { state: 'completed' } })
      .mockRejectedValueOnce(new Error('节点离线'))
      .mockResolvedValueOnce({ task_id: 'task-3', task: { state: 'completed' } })

    const wrapper = mountPage()
    await flushPromises()

    wrapper.findAllComponents({ name: 'L4RuleItem' })[0].vm.$emit('diagnose', rulesData[0])
    await flushPromises()
    const modal = wrapper.findComponent({ name: 'RuleDiagnosticModal' })
    expect(modal.props('modelValue')).toBe(true)

    modal.vm.$emit('retry')
    await flushPromises()

    expect(modal.props('modelValue')).toBe(true)
    expect(modal.props('canRetry')).toBe(true)
    expect(modal.props('ruleLabel')).toBe('0.0.0.0:8011')
    expect(modal.props('agentLabel')).toBe('master')
    expect(modal.props('task')).toEqual({
      agent_id: '1',
      state: 'failed',
      error: '节点离线'
    })
    expect(errorSpy).not.toHaveBeenCalled()

    modal.vm.$emit('retry')
    await flushPromises()
    expect(diagnoseMutateAsync).toHaveBeenCalledTimes(3)
    expect(diagnoseMutateAsync).toHaveBeenLastCalledWith({ id: 11, agentId: '1' })
    expect(modal.props('task')).toEqual({ state: 'completed' })
  })
})

describe('L4RulesPage boundary states', () => {
  it('shows ErrorState when the list fails with no data and retries via refetch', async () => {
    listState = makeListState([], { isError: ref(true), error: ref(new Error('网关超时')) })
    const wrapper = mountPage()
    await flushPromises()

    const alert = wrapper.get('[role="alert"]')
    expect(alert.text()).toContain('L4 规则加载失败')
    expect(alert.text()).toContain('网关超时')
    await alert.get('button').trigger('click')
    expect(listState.refetch).toHaveBeenCalledTimes(1)
  })

  it('distinguishes filtered no-results from truly empty data', async () => {
    listState = makeListState([])
    route = reactive({ query: { agentId: '1', enabled: 'false' } })
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('没有匹配的结果')
    expect(wrapper.text()).not.toContain('暂无 L4 规则')
    await wrapper.get('[data-testid="l4-clear-filters"]').trigger('click')
    expect(route.query.enabled).toBeUndefined()
  })

  it('shows the truly empty state without active filters', async () => {
    listState = makeListState([])
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('暂无 L4 规则')
    expect(wrapper.text()).not.toContain('没有匹配的结果')
    expect(wrapper.find('[data-testid="l4-bulk-toggle"]').exists()).toBe(false)
  })
})
