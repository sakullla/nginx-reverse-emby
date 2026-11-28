import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import RelayListenersPage from './RelayListenersPage.vue'
import RelayCard from '../components/relay/RelayCard.vue'
import { messageStore } from '../stores/messages'

let route
let routerReplace
let selectedAgentId
let agentsData
let listenersPageData
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
    systemInfo: { value: { traffic_stats_enabled: false } }
  })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: { value: agentsData } })
}))

vi.mock('../hooks/useRelayListeners', () => ({
  useRelayListenersList: () => ({
    data: { value: listenersPageData },
    isLoading: listState.isLoading,
    isError: listState.isError,
    error: listState.error,
    isFetching: listState.isFetching,
    refetch: listState.refetch
  }),
  useDeleteRelayListener: () => ({
    mutateAsync: vi.fn(),
    isPending: ref(false)
  }),
  useUpdateRelayListener: () => ({ mutate: vi.fn() })
}))

vi.mock('../hooks/useTrafficSummaryForResources', () => ({
  useTrafficSummaryForResources: () => ({ nodeTotalFor: () => 0, trafficFor: () => null })
}))

vi.mock('../api', () => ({
  fetchRelayListeners: vi.fn().mockResolvedValue([]),
  fetchAllAgentsRelayListeners: vi.fn().mockResolvedValue([]),
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
  const wrapper = mount(RelayListenersPage, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]],
      stubs: {
        RouterLink: { props: ['to'], template: '<a><slot /></a>' },
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
        TrafficTrendModal: true,
        CreateAgentPicker: true,
        BaseModal: true,
        RelayListenerForm: true,
        DeleteConfirmDialog: true,
        RelayCard: {
          name: 'RelayCard',
          props: ['listener', 'agent', 'traffic', 'agentNodeTotal'],
          template: '<div class="relay-card-stub">{{ listener.name }}</div>'
        },
        RelayTable: {
          name: 'RelayTable',
          props: ['listeners', 'agent'],
          template: '<div class="relay-table-stub" />'
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

describe('RelayListenersPage toolbar & boundary states', () => {
  beforeEach(() => {
    route = reactive({ query: { agentId: '1' } })
    routerReplace = vi.fn((target) => {
      if (target?.query) route.query = target.query
    })
    selectedAgentId = '1'
    agentsData = [{ id: 1, name: 'master' }]
    listenersPageData = { items: [], total: 0, page: 1, page_size: 20 }
  })

  it('places ViewToggle inside ResourceListFilterBar #trailing instead of header actions', async () => {
    listenersPageData = {
      items: [{ id: 1, name: 'relay-us', agent_id: 1, enabled: true, listen_host: '0.0.0.0', listen_port: 8443 }],
      total: 1,
      page: 1,
      page_size: 20
    }
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.find('.relay-page__header-right .view-toggle-stub').exists()).toBe(false)
    expect(wrapper.find('.filter-bar-stub__trailing .view-toggle-stub').exists()).toBe(true)
  })

  it('shows ErrorState when the list fails with no data and retries via refetch', async () => {
    listState.isError.value = true
    listState.error.value = new Error('中转列表超时')

    const wrapper = mountPage()
    await flushPromises()

    const error = wrapper.findComponent({ name: 'ErrorState' })
    expect(error.exists()).toBe(true)
    expect(error.text()).toContain('Relay 监听器加载失败')
    expect(error.text()).toContain('中转列表超时')
    expect(wrapper.text()).not.toContain('暂无 Relay 监听器')

    await error.get('button').trigger('click')
    expect(listState.refetch).toHaveBeenCalledTimes(1)
  })

  it('keeps existing items and toasts when a background refresh fails', async () => {
    listenersPageData = {
      items: [{ id: 1, name: 'relay-us', agent_id: 1, enabled: true, listen_host: '0.0.0.0', listen_port: 8443 }],
      total: 1,
      page: 1,
      page_size: 20
    }
    const wrapper = mountPage()
    await flushPromises()

    listState.error.value = new Error('刷新异常')
    listState.isError.value = true
    await flushPromises()

    expect(wrapper.findComponent({ name: 'ErrorState' }).exists()).toBe(false)
    expect(wrapper.find('.relay-card-stub').exists()).toBe(true)
    expect(messageStore.error).toHaveBeenCalledWith(expect.any(Error), '刷新 Relay 监听器列表失败')
  })

  it('distinguishes 0-match filters (with clear action) from a truly empty list', async () => {
    const empty = mountPage()
    await flushPromises()
    expect(empty.text()).toContain('暂无 Relay 监听器')
    expect(empty.find('[data-testid="relay-clear-filters"]').exists()).toBe(false)
    empty.unmount()
    mountedWrappers.pop()

    route.query = { agentId: '1', enabled: 'false', sync: 'pending', search: 'nonexistent' }
    const filtered = mountPage()
    await flushPromises()
    expect(filtered.text()).toContain('没有匹配的 Relay 监听器')
    expect(filtered.text()).toContain('搜索“nonexistent”')
    expect(filtered.text()).toContain('启用状态：停用')
    expect(filtered.text()).toContain('同步状态：待同步')

    await filtered.get('[data-testid="relay-clear-filters"]').trigger('click')
    await flushPromises()
    expect(routerReplace).toHaveBeenLastCalledWith({ query: { agentId: '1' } })
  })
})

describe('RelayCard public endpoint copy', () => {
  let writeText

  beforeEach(() => {
    writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true
    })
  })

  it('copies the public endpoint without triggering card edit', async () => {
    const listener = {
      id: 12,
      name: 'hk-relay',
      enabled: true,
      public_host: 'relay.example.com',
      public_port: 8443,
      listen_host: '0.0.0.0',
      listen_port: 8443,
      transport_mode: 'quic'
    }
    const wrapper = mount(RelayCard, {
      props: { listener, agent: { id: 1, name: 'master' } }
    })
    mountedWrappers.push(wrapper)

    await wrapper.get('[data-testid="relay-copy-endpoint"]').trigger('click')
    await flushPromises()

    expect(writeText).toHaveBeenCalledWith('relay.example.com:8443')
    expect(messageStore.success).toHaveBeenCalledWith('已复制公网入口')
    expect(wrapper.emitted('edit')).toBeUndefined()
  })

  it('falls back to bind_hosts when public_host is unset', async () => {
    const listener = {
      id: 13,
      name: 'jp-relay',
      enabled: true,
      bind_hosts: ['203.0.113.5'],
      listen_port: 9443
    }
    const wrapper = mount(RelayCard, {
      props: { listener, agent: { id: 1, name: 'master' } }
    })
    mountedWrappers.push(wrapper)

    await wrapper.get('[data-testid="relay-copy-endpoint"]').trigger('click')
    await flushPromises()

    expect(writeText).toHaveBeenCalledWith('203.0.113.5:9443')
  })

  it('hides the copy button when host is missing or placeholder even with a port', () => {
    const listener = {
      id: 14,
      name: 'unbound-relay',
      enabled: true,
      public_host: '',
      bind_hosts: [],
      listen_host: '',
      listen_port: 8443
    }
    const wrapper = mount(RelayCard, {
      props: { listener, agent: { id: 1, name: 'master' } }
    })
    mountedWrappers.push(wrapper)

    expect(wrapper.find('[data-testid="relay-copy-endpoint"]').exists()).toBe(false)
  })
})
