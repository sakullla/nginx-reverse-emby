import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import RulesPage from './RulesPage.vue'
import { fetchHTTPBackendProviders } from '../api'
import { describeHTTPBackends } from '../utils/httpBackend.js'

let route
let routerReplace
let selectedAgentId
let agentsData
let rulesData
let capturedListOptions
const mountedWrappers = []
const queryClients = []

function listPageResult(items) {
  const list = Array.isArray(items) ? items : []
  return {
    data: {
      value: { items: list, total: list.length, page: 1, page_size: 20 }
    },
    isLoading: ref(false)
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
  useDeleteRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: ref(false) })
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
  fetchHTTPBackendProviders: vi.fn().mockResolvedValue([])
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
          props: ['rule', 'agent', 'providerCatalog', 'providerCatalogStatus'],
          template: '<div class="rule-card-stub" />'
        },
        RuleTable: {
          name: 'RuleTable',
          props: ['rules', 'agent', 'providerCatalog', 'providerCatalogStatus'],
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
