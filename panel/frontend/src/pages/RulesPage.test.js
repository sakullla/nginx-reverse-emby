import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import RulesPage from './RulesPage.vue'

let route
let routerReplace
let selectedAgentId
let agentsData
let rulesData
let capturedListOptions

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
  fetchEgressProfiles: vi.fn().mockResolvedValue({ profiles: [{ id: 2, name: 'direct' }] })
}))

function mountPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } }
  })
  return mount(RulesPage, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]],
      stubs: {
        AgentSearchSelect: { props: ['modelValue', 'agents'], template: '<div />' },
        RuleForm: true,
        RuleCard: true,
        RuleTable: true,
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
}

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

  it('renders quick chips for enabled and sync dimensions', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const chips = wrapper.findAll('.resource-list-filter-bar__chips .resource-list-filter-bar__chip')
    expect(chips.map((chip) => chip.text())).toEqual(['启用', '停用', '已同步', '待同步'])
  })

  it('writes chip changes to the URL immediately and back into the list query', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const chip = wrapper.findAll('.resource-list-filter-bar__chips .resource-list-filter-bar__chip')[0]
    await chip.trigger('click')
    expect(routerReplace).toHaveBeenCalledWith({
      query: { agentId: '1', enabled: 'true' }
    })
    expect(capturedListOptions.enabledFilter.value).toBe(true)
  })

  it('resets the page to 1 when any filter changes', async () => {
    const wrapper = mountPage()
    await flushPromises()
    capturedListOptions.page.value = 3
    const chip = wrapper.findAll('.resource-list-filter-bar__chips .resource-list-filter-bar__chip')[2]
    await chip.trigger('click')
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
})
