import { beforeEach, describe, expect, it, vi } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import AgentsPage from './AgentsPage.vue'
import { DOC_LINKS } from '../constants/docLinks'

const createPkiEnrollmentToken = vi.fn()
let agentsData
let agentsError
let monitorData
let monitorActive
let routeQuery
const routerReplace = vi.fn()
const refetchAgents = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), replace: routerReplace }),
  useRoute: () => ({ query: routeQuery })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({
    data: agentsData,
    isLoading: ref(false),
    isError: agentsError,
    isFetching: ref(false),
    refetch: refetchAgents
  }),
  useUpdateAgent: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useDeleteAgent: () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
}))

vi.mock('../hooks/useAgentMonitorStream', () => ({
  useAgentMonitorStream: () => ({ data: monitorData, active: monitorActive })
}))

// Real filter hook so URL/query integration stays covered here.
vi.mock('../hooks/useAgentFilters', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    useAgentFilters: (agents) => actual.useAgentFilters(agents)
  }
})

vi.mock('../api', () => ({
  fetchSystemInfo: vi.fn().mockResolvedValue({ master_register_token: 'fixed-token' }),
  applyConfig: vi.fn()
}))

vi.mock('../api/pki', () => ({
  createPkiEnrollmentToken: (...args) => createPkiEnrollmentToken(...args)
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({ selectedAgentId: ref('') })
}))

vi.mock('../stores/messages', () => ({
  messageStore: { success: vi.fn(), error: vi.fn(), warning: vi.fn() }
}))

const BaseModalStub = {
  name: 'BaseModal',
  props: ['modelValue', 'closeOnClickModal'],
  emits: ['update:modelValue'],
  template: '<div><slot /></div>'
}

const EmptyStateStub = {
  name: 'EmptyState',
  props: ['title', 'description'],
  template: '<div class="empty-state-stub"><slot name="icon" /><slot /><slot name="action" /></div>'
}

describe('AgentsPage join modal', () => {
  beforeEach(() => {
    localStorage.clear()
    routeQuery = {}
    routerReplace.mockClear()
    refetchAgents.mockClear()
    agentsData = ref([])
    agentsError = ref(false)
    monitorData = ref([])
    monitorActive = ref(false)
    createPkiEnrollmentToken.mockReset()
    createPkiEnrollmentToken.mockReturnValue(new Promise(() => {}))
  })

  it('links the page header to the nodes documentation', () => {
    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub } }
    })

    const link = wrapper.get('.agents-page__docs-link')
    expect(link.attributes('href')).toBe(DOC_LINKS.agents)
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toBe('noopener')
  })

  it('can close while an enrollment-token request is still pending', async () => {
    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub } }
    })

    const joinButton = wrapper.findAll('button').find((button) => button.text().includes('加入节点'))
    await joinButton.trigger('click')

    const modal = wrapper.findComponent(BaseModalStub)
    expect(modal.props('modelValue')).toBe(true)
    expect(modal.props('closeOnClickModal')).toBe(true)
    expect(createPkiEnrollmentToken).toHaveBeenCalledTimes(1)

    modal.vm.$emit('update:modelValue', false)
    await wrapper.vm.$nextTick()
    expect(modal.props('modelValue')).toBe(false)
  })

  it('does not overlay a stale package snapshot after the monitor stream disconnects', async () => {
    const running = 'b'.repeat(64)
    agentsData.value = [{
      id: 'edge-1',
      status: 'online',
      runtime_package_version: '2.0.0',
      runtime_package_sha256: running,
      desired_package_sha256: running,
      package_sync_status: 'aligned'
    }]
    monitorData.value = [{
      id: 'edge-1',
      status: 'offline',
      runtime_package_version: '1.0.0',
      runtime_package_sha256: 'a'.repeat(64),
      desired_package_sha256: running,
      package_sync_status: 'pending'
    }]

    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub } }
    })
    const card = wrapper.findComponent({ name: 'AgentMonitorCard' })
    expect(card.props('agent')).toMatchObject({
      runtime_package_version: '2.0.0',
      runtime_package_sha256: running,
      package_sync_status: 'aligned'
    })
  })

  it('keeps realtime liveness overlays while the monitor stream is active', async () => {
    agentsData.value = [{ id: 'edge-1', status: 'offline' }]
    monitorData.value = [{ id: 'edge-1', status: 'online' }]
    monitorActive.value = true

    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub } }
    })
    expect(wrapper.findComponent({ name: 'AgentMonitorCard' }).props('agent')).toMatchObject({
      id: 'edge-1',
      status: 'online'
    })
  })

  it('separates load failure from the empty state and offers retry', async () => {
    agentsError.value = true
    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub } }
    })

    expect(wrapper.find('[data-testid="agents-error"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('暂无节点')

    await wrapper.find('[data-testid="agents-retry"]').trigger('click')
    expect(refetchAgents).toHaveBeenCalledTimes(1)
  })

  it('offers a join CTA from the genuine empty state', async () => {
    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub, EmptyState: EmptyStateStub } }
    })

    expect(wrapper.find('[data-testid="agents-error"]').exists()).toBe(false)
    const cta = wrapper.find('[data-testid="agents-empty-join"]')
    expect(cta.exists()).toBe(true)

    await cta.trigger('click')
    expect(wrapper.findComponent(BaseModalStub).props('modelValue')).toBe(true)
    expect(createPkiEnrollmentToken).toHaveBeenCalledTimes(1)
  })

  it('triggers a manual refresh from the header', async () => {
    agentsData.value = [{ id: 'edge-1', status: 'online' }]
    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub, EmptyState: EmptyStateStub } }
    })

    await wrapper.find('[data-testid="agents-refresh"]').trigger('click')
    expect(refetchAgents).toHaveBeenCalledTimes(1)
  })
})

describe('AgentsPage list UX', () => {
  beforeEach(() => {
    localStorage.clear()
    routeQuery = {}
    routerReplace.mockClear()
    refetchAgents.mockClear()
    agentsData = ref([])
    agentsError = ref(false)
    monitorData = ref([])
    monitorActive = ref(false)
    createPkiEnrollmentToken.mockReset()
    createPkiEnrollmentToken.mockReturnValue(new Promise(() => {}))
  })

  it('syncs the search term into the URL query and shows the filtered count', async () => {
    agentsData.value = [
      { id: 'edge-1', name: 'edge-1', status: 'online', last_seen_at: new Date().toISOString() },
      { id: 'core-1', name: 'core-1', status: 'online', last_seen_at: new Date().toISOString() }
    ]
    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub } }
    })

    expect(wrapper.get('[data-testid="agents-subtitle"]').text()).toContain('2 个节点')

    await wrapper.get('input[name="agent-search"]').setValue('edge')
    await nextTick()
    await nextTick()

    expect(routerReplace).toHaveBeenCalledWith(expect.objectContaining({
      query: expect.objectContaining({ search: 'edge' })
    }))
    expect(wrapper.get('[data-testid="agents-subtitle"]').text()).toContain('1 / 2 个节点')
  })

  it('paginates the list view with ListPagination', async () => {
    routeQuery = { view: 'list' }
    agentsData.value = Array.from({ length: 25 }, (_, i) => ({
      id: `a${i}`,
      name: `agent-${i}`,
      status: 'online',
      last_seen_at: new Date(Date.now() - i * 60000).toISOString()
    }))
    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub } }
    })

    const pagination = wrapper.findComponent({ name: 'ListPagination' })
    expect(pagination.exists()).toBe(true)
    expect(pagination.props('total')).toBe(25)
    expect(wrapper.findComponent({ name: 'AgentTable' }).props('agents')).toHaveLength(20)

    pagination.vm.$emit('update:page', 2)
    await nextTick()
    expect(wrapper.findComponent({ name: 'AgentTable' }).props('agents')).toHaveLength(5)
    expect(pagination.props('page')).toBe(2)
  })

  it('caps the monitor grid at the same page size', async () => {
    agentsData.value = Array.from({ length: 22 }, (_, i) => ({
      id: `a${i}`,
      name: `agent-${i}`,
      status: 'online',
      last_seen_at: new Date(Date.now() - i * 60000).toISOString()
    }))
    const wrapper = shallowMount(AgentsPage, {
      global: { stubs: { BaseModal: BaseModalStub } }
    })

    expect(wrapper.findAllComponents({ name: 'AgentMonitorCard' })).toHaveLength(20)
    expect(wrapper.findComponent({ name: 'ListPagination' }).props('total')).toBe(22)
  })
})
