import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import CertsPage from './CertsPage.vue'
import { messageStore } from '../stores/messages'

let route
let routerReplace
let selectedAgentId
let agentsData
let certsPageData
let deleteMutateAsync
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
    systemInfo: { value: null }
  })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: { value: agentsData } })
}))

vi.mock('../hooks/useCertificates', () => ({
  useCertificatesList: () => ({
    data: { value: certsPageData },
    isLoading: ref(false)
  }),
  useDeleteCertificate: () => ({
    mutateAsync: deleteMutateAsync,
    isPending: ref(false)
  }),
  useIssueCertificate: () => ({ mutate: vi.fn() })
}))

vi.mock('../api', () => ({
  fetchCertificates: vi.fn().mockResolvedValue([]),
  fetchAllAgentsCertificates: vi.fn().mockResolvedValue([])
}))

vi.mock('../stores/messages', () => ({
  messageStore: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
    warning: vi.fn()
  }
}))

function mountPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } }
  })
  queryClients.push(queryClient)
  const wrapper = mount(CertsPage, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]],
      stubs: {
        RouterLink: { props: ['to'], template: '<a><slot /></a>' },
        CertificateCenterChrome: { template: '<header><slot name="actions" /></header>' },
        OperationStatusList: true,
        ResourceListFilterBar: true,
        SkeletonList: true,
        ViewToggle: true,
        ListPagination: true,
        IdCandidateModal: true,
        CreateAgentPicker: true,
        BaseModal: true,
        CertificateForm: true,
        CertCard: {
          name: 'CertCard',
          props: ['cert', 'agent'],
          emits: ['delete'],
          template: '<div class="cert-card-stub">{{ cert.domain }}</div>'
        },
        CertTable: {
          name: 'CertTable',
          props: ['certificates', 'agent'],
          emits: ['delete'],
          template: '<div class="cert-table-stub" />'
        },
        DeleteConfirmDialog: {
          name: 'DeleteConfirmDialog',
          props: ['show', 'title', 'message', 'name', 'confirmText', 'loading', 'error'],
          emits: ['confirm', 'cancel'],
          template: `<div v-if="show" class="delete-stub">
            <span class="delete-stub__error">{{ error }}</span>
            <button class="delete-stub__confirm" @click="$emit('confirm')">confirm</button>
          </div>`
        }
      }
    }
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

afterEach(() => {
  while (mountedWrappers.length) {
    mountedWrappers.pop().unmount()
  }
  while (queryClients.length) {
    queryClients.pop().clear()
  }
  vi.clearAllMocks()
})

describe('CertsPage delete contract', () => {
  beforeEach(() => {
    route = reactive({ query: { agentId: '1' } })
    routerReplace = vi.fn()
    selectedAgentId = '1'
    agentsData = [{ id: 1, name: 'master' }]
    certsPageData = { items: [], total: 0, page: 1, page_size: 20 }
    deleteMutateAsync = vi.fn()
  })

  it('keeps the confirm dialog open with the failure reason when delete fails', async () => {
    const cert = { id: 9, domain: 'a.example.com', agent_id: 1, enabled: true, status: 'active' }
    certsPageData = { items: [cert], total: 1, page: 1, page_size: 20 }
    deleteMutateAsync.mockRejectedValue(new Error('证书被规则引用，无法删除'))

    const wrapper = mountPage()
    await flushPromises()

    wrapper.findComponent({ name: 'CertCard' }).vm.$emit('delete', cert)
    await flushPromises()

    const dialog = wrapper.findComponent({ name: 'DeleteConfirmDialog' })
    expect(dialog.props('show')).toBe(true)

    await wrapper.get('.delete-stub__confirm').trigger('click')
    await flushPromises()

    expect(deleteMutateAsync).toHaveBeenCalledWith({ id: 9, agentId: '1' })
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('error')).toBe('证书被规则引用，无法删除')
  })

  it('closes the confirm dialog only after the delete settles successfully', async () => {
    const cert = { id: 9, domain: 'a.example.com', agent_id: 1, enabled: true, status: 'active' }
    certsPageData = { items: [cert], total: 1, page: 1, page_size: 20 }
    deleteMutateAsync.mockResolvedValue({})

    const wrapper = mountPage()
    await flushPromises()

    wrapper.findComponent({ name: 'CertCard' }).vm.$emit('delete', cert)
    await flushPromises()

    const dialog = wrapper.findComponent({ name: 'DeleteConfirmDialog' })
    expect(dialog.props('show')).toBe(true)

    await wrapper.get('.delete-stub__confirm').trigger('click')
    await flushPromises()

    expect(deleteMutateAsync).toHaveBeenCalledTimes(1)
    expect(wrapper.findComponent({ name: 'DeleteConfirmDialog' }).props('show')).toBe(false)
  })

  it('explains instead of silently ignoring system Relay CA deletion', async () => {
    const cert = { id: 3, domain: 'relay-ca', agent_id: 1, enabled: true, status: 'active', tags: ['system:relay-ca'] }
    certsPageData = { items: [cert], total: 1, page: 1, page_size: 20 }

    const wrapper = mountPage()
    await flushPromises()

    wrapper.findComponent({ name: 'CertCard' }).vm.$emit('delete', cert)
    await flushPromises()

    expect(messageStore.error).toHaveBeenCalledWith(expect.stringContaining('系统 Relay CA'))
    expect(wrapper.findComponent({ name: 'DeleteConfirmDialog' }).props('show')).toBe(false)
    expect(deleteMutateAsync).not.toHaveBeenCalled()
  })

  it('surfaces a message instead of throwing when a mutation lacks an agent', async () => {
    const cert = { id: 9, domain: 'a.example.com', enabled: true, status: 'active' }
    route.query = { agentId: '__all__' }
    selectedAgentId = '__all__'
    agentsData = [
      { id: 1, name: 'master' },
      { id: 2, name: 'edge' }
    ]
    certsPageData = { items: [cert], total: 1, page: 1, page_size: 20 }

    const wrapper = mountPage()
    await flushPromises()

    const card = wrapper.findComponent({ name: 'CertCard' })
    expect(() => card.vm.$emit('delete', cert)).not.toThrow()
    await flushPromises()

    const dialog = wrapper.findComponent({ name: 'DeleteConfirmDialog' })
    expect(dialog.props('show')).toBe(true)
    await wrapper.get('.delete-stub__confirm').trigger('click')
    await flushPromises()

    expect(messageStore.error).toHaveBeenCalledWith(expect.stringContaining('缺少节点归属'))
    expect(deleteMutateAsync).not.toHaveBeenCalled()
    expect(dialog.props('show')).toBe(true)
  })
})
