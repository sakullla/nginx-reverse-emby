import { beforeEach, describe, expect, it, vi } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import { computed, ref } from 'vue'
import AgentsPage from './AgentsPage.vue'

const createPkiEnrollmentToken = vi.fn()
let agentsData
let monitorData
let monitorActive

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: agentsData, isLoading: ref(false) }),
  useUpdateAgent: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useDeleteAgent: () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
}))

vi.mock('../hooks/useAgentMonitorStream', () => ({
  useAgentMonitorStream: () => ({ data: monitorData, active: monitorActive })
}))

vi.mock('../hooks/useAgentFilters', () => ({
  useAgentFilters: (agents) => ({
    view: ref('monitor'),
    statusFilter: ref('all'),
    modeFilter: ref('all'),
    tagFilter: ref('all'),
    sortField: ref('name'),
    sortOrder: ref('asc'),
    searchQuery: ref(''),
    availableTags: computed(() => []),
    filteredAgents: computed(() => agents.value || []),
    hasActiveFilters: computed(() => false),
    clearFilters: vi.fn(),
    toggleSortOrder: vi.fn()
  })
}))

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

describe('AgentsPage join modal', () => {
  beforeEach(() => {
    agentsData = ref([])
    monitorData = ref([])
    monitorActive = ref(false)
    createPkiEnrollmentToken.mockReset()
    createPkiEnrollmentToken.mockReturnValue(new Promise(() => {}))
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
})
