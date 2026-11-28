import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import CertsPage from './CertsPage.vue'
import CertCard from '../components/certs/CertCard.vue'
import { messageStore } from '../stores/messages'

let route
let routerReplace
let selectedAgentId
let agentsData
let certsPageData
let deleteMutateAsync
let listState
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
    isLoading: listState.isLoading,
    isError: listState.isError,
    error: listState.error,
    isFetching: listState.isFetching,
    refetch: listState.refetch
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
        CertificateCenterChrome: { template: '<header class="cert-chrome-stub"><slot name="actions" /></header>' },
        OperationStatusList: true,
        ResourceListFilterBar: {
          name: 'ResourceListFilterBar',
          props: ['agentId', 'agentBaseline', 'agents', 'q', 'searchPlaceholder', 'filterFields', 'filterValues'],
          template: '<div class="filter-bar-stub"><div class="filter-bar-stub__trailing"><slot name="trailing" /></div></div>'
        },
        SkeletonList: true,
        ViewToggle: {
          name: 'ViewToggle',
          props: ['view'],
          template: '<div class="view-toggle-stub" />'
        },
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

beforeEach(() => {
  listState = {
    isLoading: ref(false),
    isError: ref(false),
    error: ref(null),
    isFetching: ref(false),
    refetch: vi.fn()
  }
})

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

describe('CertsPage toolbar & boundary states', () => {
  beforeEach(() => {
    route = reactive({ query: { agentId: '1' } })
    routerReplace = vi.fn((target) => {
      if (target?.query) route.query = target.query
    })
    selectedAgentId = '1'
    agentsData = [{ id: 1, name: 'master' }]
    certsPageData = { items: [], total: 0, page: 1, page_size: 20 }
    deleteMutateAsync = vi.fn()
  })

  it('places ViewToggle inside ResourceListFilterBar #trailing instead of header actions', async () => {
    certsPageData = {
      items: [{ id: 1, domain: 'a.example.com', agent_id: 1, enabled: true, status: 'active' }],
      total: 1,
      page: 1,
      page_size: 20
    }
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.find('.cert-chrome-stub .view-toggle-stub').exists()).toBe(false)
    expect(wrapper.find('.filter-bar-stub__trailing .view-toggle-stub').exists()).toBe(true)
  })

  it('shows ErrorState when the list fails with no data and retries via refetch', async () => {
    listState.isError.value = true
    listState.error.value = new Error('证书接口超时')

    const wrapper = mountPage()
    await flushPromises()

    const error = wrapper.findComponent({ name: 'ErrorState' })
    expect(error.exists()).toBe(true)
    expect(error.text()).toContain('证书列表加载失败')
    expect(error.text()).toContain('证书接口超时')
    expect(wrapper.text()).not.toContain('暂无证书')

    await error.get('button').trigger('click')
    expect(listState.refetch).toHaveBeenCalledTimes(1)
  })

  it('keeps existing items and toasts when a background refresh fails', async () => {
    certsPageData = {
      items: [{ id: 1, domain: 'a.example.com', agent_id: 1, enabled: true, status: 'active' }],
      total: 1,
      page: 1,
      page_size: 20
    }
    const wrapper = mountPage()
    await flushPromises()

    listState.error.value = new Error('网络波动')
    listState.isError.value = true
    await flushPromises()

    expect(wrapper.findComponent({ name: 'ErrorState' }).exists()).toBe(false)
    expect(wrapper.find('.cert-card-stub').exists()).toBe(true)
    expect(messageStore.error).toHaveBeenCalledWith(expect.any(Error), '刷新证书列表失败')
  })

  it('distinguishes 0-match filters (with clear action) from a truly empty list', async () => {
    const empty = mountPage()
    await flushPromises()
    expect(empty.text()).toContain('暂无证书')
    expect(empty.find('[data-testid="certs-clear-filters"]').exists()).toBe(false)
    empty.unmount()
    mountedWrappers.pop()

    route.query = { agentId: '1', status: 'error', search: 'missing.example.com' }
    const filtered = mountPage()
    await flushPromises()
    expect(filtered.text()).toContain('没有匹配的证书')
    expect(filtered.text()).toContain('搜索“missing.example.com”')
    expect(filtered.text()).toContain('证书状态：签发失败')

    await filtered.get('[data-testid="certs-clear-filters"]').trigger('click')
    await flushPromises()
    expect(routerReplace).toHaveBeenLastCalledWith({ query: { agentId: '1' } })
  })
})

describe('CertCard domain copy', () => {
  let writeText

  beforeEach(() => {
    writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true
    })
  })

  it('copies the certificate domain without triggering card edit', async () => {
    const cert = { id: 7, domain: 'media.example.com', enabled: true, status: 'active' }
    const wrapper = mount(CertCard, {
      props: { cert, agent: { id: 1, name: 'master' } }
    })
    mountedWrappers.push(wrapper)

    const copyBtn = wrapper.get('[data-testid="cert-copy-domain"]')
    await copyBtn.trigger('click')
    await flushPromises()

    expect(writeText).toHaveBeenCalledWith('media.example.com')
    expect(messageStore.success).toHaveBeenCalledWith('已复制域名')
    expect(wrapper.emitted('edit')).toBeUndefined()
  })

  it.each(['', '-', '—'])('hides the copy button when domain is empty or placeholder (%s)', (domain) => {
    const cert = { id: 8, domain, enabled: true, status: 'active' }
    const wrapper = mount(CertCard, {
      props: { cert, agent: { id: 1, name: 'master' } }
    })
    mountedWrappers.push(wrapper)

    expect(wrapper.find('[data-testid="cert-copy-domain"]').exists()).toBe(false)
  })
})
