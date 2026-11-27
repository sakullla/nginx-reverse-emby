import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import RulesPage from './RulesPage.vue'
import { fetchHTTPBackendProviders } from '../api'
import { describeHTTPBackends } from '../utils/httpBackend.js'
import { DOC_LINKS } from '../constants/docLinks'

let route
let routerReplace
let selectedAgentId
let agentsData
let rulesData
let capturedListOptions
let listState
let deleteMutateAsync
let listDataRef
const mountedWrappers = []
const queryClients = []

function listPageResult(items) {
  const list = Array.isArray(items) ? items : []
  listDataRef = ref({ items: list, total: list.length, page: 1, page_size: 20 })
  return {
    data: listDataRef,
    isLoading: ref(false),
    isError: listState.isError,
    error: listState.error,
    isFetching: ref(false),
    refetch: listState.refetch
  }
}

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

vi.mock('../hooks/useRules', () => ({
  useRulesList: (options) => {
    capturedListOptions = options
    return listPageResult(rulesData)
  },
  useCreateRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useUpdateRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useDeleteRule: () => ({ mutate: vi.fn(), mutateAsync: (...args) => deleteMutateAsync(...args), isPending: ref(false) })
}))

vi.mock('../hooks/useDiagnostics', () => ({
  useDiagnoseRule: () => ({ mutateAsync: vi.fn() }),
  useDiagnosticTask: () => ({ data: ref(null) })
}))

vi.mock('../hooks/useTrafficSummaryForResources', () => ({
  useTrafficSummaryForResources: () => ({ nodeTotalFor: () => 0, trafficFor: () => null })
}))

vi.mock('../api', () => ({
  fetchRules: vi.fn().mockResolvedValue([{ id: 1, tags: ['emby', 'web'] }]),
  fetchAllAgentsRules: vi.fn().mockResolvedValue([]),
  fetchCertificates: vi.fn().mockResolvedValue([{ id: 7, domain: 'example.com' }]),
  fetchAllAgentsCertificates: vi.fn().mockResolvedValue([]),
  fetchRelayListeners: vi.fn().mockResolvedValue([{ id: 3, name: 'relay-a' }]),
  fetchAllAgentsRelayListeners: vi.fn().mockResolvedValue([]),
  fetchEgressProfiles: vi.fn().mockResolvedValue({ profiles: [{ id: 2, name: 'direct' }] }),
  fetchHTTPBackendProviders: vi.fn().mockResolvedValue([]),
  updateRule: vi.fn().mockResolvedValue({}),
  deleteRule: vi.fn().mockResolvedValue({})
}))

function mountPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } }
  })
  queryClients.push(queryClient)
  const wrapper = mount(RulesPage, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]],
      stubs: {
        RouterLink: { props: ['to'], template: '<a><slot /></a>' },
        AgentSearchSelect: { props: ['modelValue', 'agents'], template: '<div />' },
        RuleForm: true,
        RuleCard: {
          name: 'RuleCard',
          props: ['rule', 'agent', 'providerCatalog', 'providerCatalogStatus', 'selectable', 'selected'],
          template: '<div class="rule-card-stub" />'
        },
        RuleTable: {
          name: 'RuleTable',
          props: ['rules', 'agent', 'providerCatalog', 'providerCatalogStatus', 'selectable', 'selectedKeys', 'allSelected', 'indeterminate'],
          template: '<div class="rule-table-stub" />'
        },
        BaseModal: true,
        DeleteConfirmDialog: true,
        RuleDiagnosticModal: true,
        TrafficTrendModal: true,
        IdCandidateModal: true,
        CreateAgentPicker: true,
        ViewToggle: true,
        OperationStatusList: true,
        ListPagination: true
      }
    }
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

function teleportedPanelQuery(selector) {
  return document.body.querySelector(`.resource-list-filter-bar__panel ${selector}`)
}

beforeEach(() => {
  listState = { isError: ref(false), error: ref(null), refetch: vi.fn() }
  deleteMutateAsync = vi.fn().mockResolvedValue({})
})

afterEach(() => {
  while (mountedWrappers.length) {
    mountedWrappers.pop().unmount()
  }
  while (queryClients.length) {
    queryClients.pop().clear()
  }
  document.body.innerHTML = ''
})

describe('RulesPage filter integration', () => {
  beforeEach(() => {
    route = reactive({ query: { agentId: '1' } })
    routerReplace = vi.fn((target) => {
      if (target?.query) route.query = target.query
    })
    selectedAgentId = '1'
    agentsData = [{ id: 1, name: 'master' }]
    rulesData = [{ id: 1, agent_id: 1, enabled: true, tags: ['emby'] }]
    capturedListOptions = undefined
    fetchHTTPBackendProviders.mockReset().mockResolvedValue([])
  })

  it('links the page header to the HTTP rules documentation', async () => {
    const wrapper = mountPage()
    await flushPromises()

    const link = wrapper.get('.rules-page__docs-link')
    expect(link.attributes('href')).toBe(DOC_LINKS.httpRules)
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toBe('noopener')
  })

  it('opens the create form directly when arriving from the onboarding checklist', async () => {
    route = reactive({ query: { agentId: '1', create: '1' } })
    const wrapper = mountPage()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ query: { agentId: '1' } })
    expect(route.query.create).toBeUndefined()
    const openModal = wrapper
      .findAllComponents({ name: 'BaseModal' })
      .some((modal) => modal.props('modelValue') === true)
    expect(openModal).toBe(true)
  })

  it('consumes filter state from the URL into the list query', () => {
    route.query = { agentId: '1', enabled: 'true', tags: 'emby,web', sync: 'pending', cert: '7' }
    mountPage()
    expect(capturedListOptions.enabledFilter.value).toBe(true)
    expect(capturedListOptions.filters.value).toEqual({
      tags: ['emby', 'web'],
      sync: 'pending',
      certificateId: '7'
    })
  })

  it('silently ignores invalid URL filter values', () => {
    route.query = { agentId: '1', enabled: '999', sync: 'bogus', cert: '-3' }
    mountPage()
    expect(capturedListOptions.enabledFilter.value).toBeUndefined()
    // page-level raw filter object stays all-empty; useResourceListQuery drops empty values
    expect(capturedListOptions.filters.value).toEqual({
      tags: [],
      sync: undefined,
      certificateId: undefined,
      egressProfileId: undefined,
      relayListenerId: undefined
    })
  })

  it('keeps #id= deep links out of the server-side q param', () => {
    route.query = { agentId: '1', search: '#id=5' }
    mountPage()
    expect(capturedListOptions.q.value).toBe('')
  })

  it('writes status filter changes from the panel into the URL and list query', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.find('.resource-list-filter-bar__filter-trigger').trigger('click')
    teleportedPanelQuery(
      '[data-field-key="enabled"] .resource-list-filter-bar__segment[data-value="true"]'
    ).click()
    await flushPromises()
    expect(routerReplace).toHaveBeenCalledWith({
      query: { agentId: '1', enabled: 'true' }
    })
    expect(capturedListOptions.enabledFilter.value).toBe(true)
  })

  it('resets the page to 1 when any filter changes', async () => {
    const wrapper = mountPage()
    await flushPromises()
    capturedListOptions.page.value = 3
    await wrapper.find('.resource-list-filter-bar__filter-trigger').trigger('click')
    teleportedPanelQuery(
      '[data-field-key="sync"] .resource-list-filter-bar__segment[data-value="applied"]'
    ).click()
    await flushPromises()
    expect(capturedListOptions.page.value).toBe(1)
  })

  it('offers tag options from the agent rules plus select options for related resources', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const fields = wrapper.findComponent({ name: 'ResourceListFilterBar' })
    const filterFields = fields.props('filterFields')
    const byKey = Object.fromEntries(filterFields.map((field) => [field.key, field]))
    expect(byKey.tags.type).toBe('multi')
    expect(byKey.tags.options.map((option) => option.value)).toContain('emby')
    expect(byKey.certificate_id.options.map((option) => option.label)).toContain('example.com')
    expect(byKey.relay_listener_id.options.map((option) => option.label)).toContain('relay-a')
    expect(byKey.egress_profile_id.options.map((option) => option.label)).toContain('direct')
  })

  it('flattens all-agents group payloads into tag and related-resource options', async () => {
    const api = await import('../api')
    api.fetchAllAgentsRules.mockResolvedValueOnce([
      { agentId: '1', rules: [{ id: 11, tags: ['emby'] }] },
      { agentId: '2', rules: [{ id: 12, tags: ['media', 'edge'] }] }
    ])
    api.fetchAllAgentsCertificates.mockResolvedValueOnce([
      { agentId: '1', certificates: [{ id: 71, domain: 'a.example.com' }] },
      { agentId: '2', certificates: [{ id: 72, domain: 'b.example.com' }] }
    ])
    api.fetchAllAgentsRelayListeners.mockResolvedValueOnce([
      { agentId: '1', listeners: [{ id: 31, name: 'relay-west' }] },
      { agentId: '2', listeners: [{ id: 32, name: 'relay-east' }] }
    ])

    route.query = { agentId: '__all__' }
    selectedAgentId = '__all__'
    agentsData = [
      { id: '1', name: 'master' },
      { id: '2', name: 'edge' }
    ]

    const wrapper = mountPage()
    await flushPromises()

    const fields = wrapper.findComponent({ name: 'ResourceListFilterBar' })
    const byKey = Object.fromEntries(fields.props('filterFields').map((field) => [field.key, field]))
    expect(byKey.tags.options.map((option) => option.value).sort()).toEqual(['edge', 'emby', 'media'])
    expect(byKey.certificate_id.options.map((option) => option.label)).toEqual(
      expect.arrayContaining(['a.example.com', 'b.example.com'])
    )
    expect(byKey.relay_listener_id.options.map((option) => option.label)).toEqual(
      expect.arrayContaining(['relay-west', 'relay-east'])
    )
  })

  it('updates multi tags through the filter event and URL round-trip', async () => {
    const wrapper = mountPage()
    await flushPromises()
    wrapper.findComponent({ name: 'ResourceListFilterBar' }).vm.$emit('update:filter', { key: 'tags', value: ['emby'] })
    await flushPromises()
    expect(routerReplace).toHaveBeenCalledWith({
      query: { agentId: '1', tags: 'emby' }
    })
    expect(capturedListOptions.filters.value).toEqual({ tags: ['emby'] })
  })

  it('keeps provider status unknown after a single-Agent error and recovers on retry', async () => {
    rulesData = [{
      id: 7,
      agent_id: '1',
      enabled: true,
      backends: [{ kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator', provider_id: 'default' } }]
    }]
    fetchHTTPBackendProviders
      .mockRejectedValueOnce(new Error('catalog unavailable'))
      .mockResolvedValueOnce([{
        instance_id: 'accelerator',
        provider_id: 'default',
        display_name: '加速源',
        state: 'active',
        ready_generation: 'generation-8'
      }])

    const wrapper = mountPage()
    await vi.waitFor(() => expect(wrapper.text()).toContain('插件状态加载失败'))
    let card = wrapper.findComponent({ name: 'RuleCard' })
    expect(card.props('providerCatalogStatus')).toBe('error')
    expect(card.props('providerCatalog')).toEqual([])
    expect(describeHTTPBackends(rulesData[0], [], 'error')[0].state).toBe('unknown')

    await wrapper.get('.provider-catalog-notice button').trigger('click')
    await vi.waitFor(() => {
      expect(wrapper.findComponent({ name: 'RuleCard' }).props('providerCatalogStatus')).toBe('ready')
    })
    card = wrapper.findComponent({ name: 'RuleCard' })
    expect(card.props('providerCatalogStatus')).toBe('ready')
    expect(card.props('providerCatalog')).toEqual([expect.objectContaining({
      agent_id: '1',
      instance_id: 'accelerator',
      ready_generation: 'generation-8'
    })])
  })

  it('aggregates all-Agent catalogs without cross-Agent provider collisions', async () => {
    route.query = { agentId: '__all__' }
    selectedAgentId = '__all__'
    agentsData = [{ id: 'edge-a', name: 'A' }, { id: 'edge-b', name: 'B' }]
    rulesData = ['edge-a', 'edge-b'].map((ruleAgentId, index) => ({
      id: index + 1,
      agent_id: ruleAgentId,
      enabled: true,
      backends: [{ kind: 'plugin_provider', plugin_provider: { instance_id: 'shared', provider_id: 'default' } }]
    }))
    fetchHTTPBackendProviders.mockImplementation(async (targetAgentId) => [{
      instance_id: 'shared',
      provider_id: 'default',
      display_name: `Provider ${targetAgentId}`,
      state: targetAgentId === 'edge-a' ? 'active' : 'inactive',
      ready_generation: `generation-${targetAgentId}`
    }])

    const wrapper = mountPage()
    await vi.waitFor(() => {
      expect(wrapper.findComponent({ name: 'RuleTable' }).props('providerCatalogStatus')).toBe('ready')
    })
    const catalog = wrapper.findComponent({ name: 'RuleTable' }).props('providerCatalog')
    expect(catalog.map((provider) => provider.agent_id).sort()).toEqual(['edge-a', 'edge-b'])
    expect(describeHTTPBackends(rulesData[0], catalog, 'ready')[0]).toMatchObject({
      label: 'Provider edge-a', state: 'active', generation: 'generation-edge-a'
    })
    expect(describeHTTPBackends(rulesData[1], catalog, 'ready')[0]).toMatchObject({
      label: 'Provider edge-b', state: 'inactive', generation: 'generation-edge-b'
    })
  })

  it('keeps published-port catalog entries on their reporting Agent', async () => {
    route.query = { agentId: '__all__' }
    selectedAgentId = '__all__'
    agentsData = [{ id: 'edge-a', name: 'A' }, { id: 'edge-b', name: 'B' }]
    rulesData = [{
      id: 1,
      agent_id: 'edge-a',
      enabled: true,
      backends: [{ url: 'http://127.0.0.1:5000' }]
    }]
    fetchHTTPBackendProviders.mockImplementation(async (targetAgentId) => (
      targetAgentId === 'edge-a'
        ? [{
            kind: 'published_port',
            instance_id: 'control-1',
            display_name: 'hubproxy',
            resource_id: 'hubproxy',
            port: 5000,
            state: 'active'
          }]
        : []
    ))

    const wrapper = mountPage()
    await vi.waitFor(() => {
      expect(wrapper.findComponent({ name: 'RuleTable' }).props('providerCatalogStatus')).toBe('ready')
    })
    const catalog = wrapper.findComponent({ name: 'RuleTable' }).props('providerCatalog')
    expect(catalog).toEqual([expect.objectContaining({
      agent_id: 'edge-a',
      kind: 'published_port',
      resource_id: 'hubproxy',
      port: 5000
    })])
    expect(catalog.some((item) => item.agent_id === 'edge-b')).toBe(false)
    expect(describeHTTPBackends(rulesData[0], catalog, 'ready')[0]).toMatchObject({
      kind: 'url',
      label: 'http://127.0.0.1:5000'
    })
  })
})

describe('RulesPage bulk mode', () => {
  beforeEach(async () => {
    route = reactive({ query: { agentId: '1' } })
    routerReplace = vi.fn((target) => {
      if (target?.query) route.query = target.query
    })
    selectedAgentId = '1'
    agentsData = [{ id: 1, name: 'master' }]
    rulesData = [
      { id: 1, agent_id: 1, enabled: true, frontend_url: 'https://a.example.com', tags: [] },
      { id: 2, agent_id: 1, enabled: true, frontend_url: 'https://b.example.com', tags: [] },
      { id: 3, agent_id: 1, enabled: false, frontend_url: 'https://c.example.com', tags: [] }
    ]
    fetchHTTPBackendProviders.mockReset().mockResolvedValue([])
    const api = await import('../api')
    api.updateRule.mockReset().mockResolvedValue({})
    api.deleteRule.mockReset().mockResolvedValue({})
  })

  async function enterBulk(wrapper) {
    await wrapper.get('[data-testid="rules-bulk-toggle"]').trigger('click')
    await flushPromises()
  }

  function cards(wrapper) {
    return wrapper.findAllComponents({ name: 'RuleCard' })
  }

  it('enters selection mode, selects via cards and the table select-all, and exits with Esc', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.findComponent({ name: 'BulkActionBar' }).exists()).toBe(false)

    await enterBulk(wrapper)
    expect(cards(wrapper).every((card) => card.props('selectable') === true)).toBe(true)
    const table = wrapper.findComponent({ name: 'RuleTable' })
    expect(table.props('selectable')).toBe(true)

    cards(wrapper)[0].vm.$emit('select', rulesData[0])
    await flushPromises()
    expect(cards(wrapper)[0].props('selected')).toBe(true)
    expect(table.props('indeterminate')).toBe(true)
    expect(wrapper.get('.bulk-action-bar__count').text()).toBe('已选 1 项')

    table.vm.$emit('select-all')
    await flushPromises()
    expect(table.props('allSelected')).toBe(true)
    expect(table.props('selectedKeys')).toEqual(['1:1', '1:2', '1:3'])

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(wrapper.findComponent({ name: 'BulkActionBar' }).exists()).toBe(false)
    expect(table.props('selectable')).toBe(false)
    expect(table.props('selectedKeys')).toEqual([])
  })

  it('cancel in the bulk bar exits and clears the selection', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await enterBulk(wrapper)
    cards(wrapper)[1].vm.$emit('select', rulesData[1])
    await flushPromises()
    await wrapper.get('[data-bulk-action="cancel"]').trigger('click')
    await flushPromises()
    expect(wrapper.findComponent({ name: 'BulkActionBar' }).exists()).toBe(false)
    await enterBulk(wrapper)
    expect(wrapper.get('.bulk-action-bar__count').text()).toBe('已选 0 项')
  })

  it('bulk disable calls updateRule(enabled=false) per rule and keeps failed ones selected', async () => {
    const api = await import('../api')
    api.updateRule.mockImplementation(async (_agent, id) => {
      if (id === 2) throw new Error('权限不足')
      return {}
    })
    const { messageStore } = await import('../stores/messages')
    const errorSpy = vi.spyOn(messageStore, 'error')

    const wrapper = mountPage()
    await flushPromises()
    await enterBulk(wrapper)
    cards(wrapper)[0].vm.$emit('select', rulesData[0])
    cards(wrapper)[1].vm.$emit('select', rulesData[1])
    await flushPromises()
    await wrapper.get('[data-bulk-action="disable"]').trigger('click')
    await flushPromises()

    expect(api.updateRule).toHaveBeenCalledTimes(2)
    expect(api.updateRule).toHaveBeenCalledWith('1', 1, { enabled: false })
    expect(api.updateRule).toHaveBeenCalledWith('1', 2, { enabled: false })
    expect(wrapper.findComponent({ name: 'RuleTable' }).props('selectedKeys')).toEqual(['1:2'])
    expect(errorSpy).toHaveBeenCalledTimes(1)
    expect(errorSpy.mock.calls[0][0]).toBe('已禁用 1 项，失败 1 项：权限不足')
    errorSpy.mockRestore()
  })

  it('bulk delete asks for confirmation with the count before deleting', async () => {
    const api = await import('../api')
    const wrapper = mountPage()
    await flushPromises()
    await enterBulk(wrapper)
    wrapper.findComponent({ name: 'RuleTable' }).vm.$emit('select-all')
    await flushPromises()
    await wrapper.get('[data-bulk-action="delete"]').trigger('click')
    await flushPromises()

    expect(api.deleteRule).not.toHaveBeenCalled()
    const dialog = wrapper.findAllComponents({ name: 'DeleteConfirmDialog' })
      .find((item) => item.props('title') === '确认批量删除规则')
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('message')).toContain('3 条')

    dialog.vm.$emit('confirm')
    await flushPromises()
    expect(api.deleteRule.mock.calls.map((call) => call[1]).sort()).toEqual([1, 2, 3])
    expect(dialog.props('show')).toBe(false)
    expect(wrapper.findComponent({ name: 'RuleTable' }).props('selectedKeys')).toEqual([])
  })

  it('shows a partial bulk delete failure inside the dialog and keeps the failed rule selected', async () => {
    const api = await import('../api')
    api.deleteRule.mockImplementation(async (_agent, id) => {
      if (id === 3) throw new Error('规则被引用')
      return {}
    })
    const wrapper = mountPage()
    await flushPromises()
    await enterBulk(wrapper)
    wrapper.findComponent({ name: 'RuleTable' }).vm.$emit('select-all')
    await flushPromises()
    await wrapper.get('[data-bulk-action="delete"]').trigger('click')
    const dialog = wrapper.findAllComponents({ name: 'DeleteConfirmDialog' })
      .find((item) => item.props('title') === '确认批量删除规则')
    dialog.vm.$emit('confirm')
    await flushPromises()
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('error')).toBe('已删除 2 项，失败 1 项：规则被引用')
    expect(wrapper.findComponent({ name: 'RuleTable' }).props('selectedKeys')).toEqual(['1:3'])
  })

  it('drops selected keys that disappear after a background refetch', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await enterBulk(wrapper)
    wrapper.findComponent({ name: 'RuleTable' }).vm.$emit('select-all')
    await flushPromises()
    expect(wrapper.get('.bulk-action-bar__count').text()).toBe('已选 3 项')

    const remaining = rulesData.filter((rule) => rule.id !== 2)
    listDataRef.value = { items: remaining, total: remaining.length, page: 1, page_size: 20 }
    await flushPromises()
    expect(wrapper.findComponent({ name: 'RuleTable' }).props('selectedKeys')).toEqual(['1:1', '1:3'])
    expect(wrapper.get('.bulk-action-bar__count').text()).toBe('已选 2 项')
  })

  it.each([
    ['agent', () => { route.query = { ...route.query, agentId: '__all__' } }],
    ['search', () => { route.query = { ...route.query, search: 'abc' } }],
    ['filter', () => { route.query = { ...route.query, enabled: 'true' } }],
    ['page', () => { capturedListOptions.page.value = 2 }]
  ])('clears the selection when the %s changes', async (_label, change) => {
    const wrapper = mountPage()
    await flushPromises()
    await enterBulk(wrapper)
    cards(wrapper)[0].vm.$emit('select', rulesData[0])
    await flushPromises()
    expect(wrapper.findComponent({ name: 'RuleTable' }).props('selectedKeys')).toEqual(['1:1'])
    change()
    await flushPromises()
    expect(wrapper.findComponent({ name: 'RuleTable' }).props('selectedKeys')).toEqual([])
  })
})

describe('RulesPage boundary states', () => {
  beforeEach(() => {
    route = reactive({ query: { agentId: '1' } })
    routerReplace = vi.fn((target) => {
      if (target?.query) route.query = target.query
    })
    selectedAgentId = '1'
    agentsData = [{ id: 1, name: 'master' }]
    rulesData = []
    fetchHTTPBackendProviders.mockReset().mockResolvedValue([])
  })

  it('shows ErrorState when the list fails with no data and retries via refetch', async () => {
    listState.isError.value = true
    listState.error.value = new Error('网关超时')
    const wrapper = mountPage()
    await flushPromises()
    const error = wrapper.findComponent({ name: 'ErrorState' })
    expect(error.exists()).toBe(true)
    expect(error.text()).toContain('网关超时')
    expect(wrapper.text()).not.toContain('暂无规则')
    await error.get('button').trigger('click')
    expect(listState.refetch).toHaveBeenCalledTimes(1)
  })

  it('distinguishes no matches (with clear action) from a truly empty list', async () => {
    const empty = mountPage()
    await flushPromises()
    expect(empty.text()).toContain('暂无规则')
    expect(empty.find('[data-testid="rules-clear-filters"]').exists()).toBe(false)
    empty.unmount()
    mountedWrappers.pop()

    route.query = { agentId: '1', enabled: 'false', search: 'emby' }
    const filtered = mountPage()
    await flushPromises()
    expect(filtered.text()).toContain('没有匹配的结果')
    expect(filtered.text()).toContain('搜索“emby”')
    expect(filtered.text()).toContain('启用状态：停用')
    await filtered.get('[data-testid="rules-clear-filters"]').trigger('click')
    await flushPromises()
    expect(routerReplace).toHaveBeenLastCalledWith({ query: { agentId: '1' } })
  })

  it('shows a single-delete failure inside the delete dialog', async () => {
    rulesData = [{ id: 9, agent_id: 1, enabled: true, frontend_url: 'https://x.example.com', tags: [] }]
    deleteMutateAsync = vi.fn().mockRejectedValue(new Error('规则正在使用'))
    const wrapper = mountPage()
    await flushPromises()
    wrapper.findComponent({ name: 'RuleCard' }).vm.$emit('delete', rulesData[0])
    await flushPromises()
    const dialog = wrapper.findAllComponents({ name: 'DeleteConfirmDialog' })
      .find((item) => item.props('title') === '确认删除规则')
    expect(dialog.props('show')).toBe(true)
    dialog.vm.$emit('confirm')
    await flushPromises()
    expect(deleteMutateAsync).toHaveBeenCalledWith({ id: 9, agentId: '1' })
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('error')).toBe('规则正在使用')
  })
})
