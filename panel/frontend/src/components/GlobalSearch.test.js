import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GlobalSearch from './GlobalSearch.vue'

const mocks = vi.hoisted(() => ({
  push: vi.fn(),
  agentsData: {
    value: [
    { id: 'edge-1', name: 'edge-1', status: 'online' }
    ]
  },
  fetchAllAgentsRules: vi.fn(),
  fetchAllAgentsL4Rules: vi.fn(),
  fetchAllAgentsCertificates: vi.fn(),
  fetchAllAgentsRelayListeners: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: mocks.agentsData })
}))

vi.mock('../api', () => ({
  fetchAllAgentsRules: mocks.fetchAllAgentsRules,
  fetchAllAgentsL4Rules: mocks.fetchAllAgentsL4Rules,
  fetchAllAgentsCertificates: mocks.fetchAllAgentsCertificates,
  fetchAllAgentsRelayListeners: mocks.fetchAllAgentsRelayListeners
}))

function mountSearch() {
  activeWrapper = mount(GlobalSearch, {
    props: { open: true },
    attachTo: document.body,
    global: {
      stubs: {
        Teleport: true
      }
    }
  })
  return activeWrapper
}

let activeWrapper = null

describe('GlobalSearch exact ID results', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mocks.push.mockReset()
    mocks.fetchAllAgentsRules.mockResolvedValue([])
    mocks.fetchAllAgentsL4Rules.mockResolvedValue([])
    mocks.fetchAllAgentsCertificates.mockResolvedValue([])
    mocks.fetchAllAgentsRelayListeners.mockResolvedValue([
      { agentId: 'edge-1', listeners: [{ id: 77, name: 'relay-target' }] }
    ])
  })

  afterEach(() => {
    activeWrapper?.unmount()
    activeWrapper = null
    vi.clearAllTimers()
    vi.useRealTimers()
    document.body.innerHTML = ''
  })

  it('does not show relay listener matches for exact ID searches', async () => {
    const wrapper = mountSearch()

    await wrapper.get('input[name="global-search"]').setValue('#id=77')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()

    expect(wrapper.text()).toContain('未找到匹配结果')
    expect(wrapper.text()).not.toContain('relay-target')
  })
})

describe('GlobalSearch group routing and partial failures', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mocks.push.mockReset()
    mocks.fetchAllAgentsRules.mockResolvedValue([])
    mocks.fetchAllAgentsL4Rules.mockResolvedValue([])
    mocks.fetchAllAgentsCertificates.mockResolvedValue([])
    mocks.fetchAllAgentsRelayListeners.mockResolvedValue([])
  })

  afterEach(() => {
    activeWrapper?.unmount()
    activeWrapper = null
    vi.clearAllTimers()
    vi.useRealTimers()
    document.body.innerHTML = ''
  })

  it('routes group header clicks by group type', async () => {
    mocks.fetchAllAgentsRules.mockResolvedValue([
      { agentId: 'edge-1', rules: [{ id: 1, frontend_url: 'edge.example.com' }] }
    ])
    const wrapper = mountSearch()
    await wrapper.get('input[name="global-search"]').setValue('edge')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()

    const agentHeader = wrapper.findAll('.result-group__header')
      .find((header) => header.text().includes('节点'))
    expect(agentHeader).toBeTruthy()
    await agentHeader.trigger('click')
    expect(mocks.push).toHaveBeenCalledWith('/agents')

    await wrapper.get('input[name="global-search"]').setValue('edge')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    const agentGroupHeader = wrapper.findAll('.result-group__header')
      .find((header) => header.text().includes('edge-1'))
    expect(agentGroupHeader).toBeTruthy()
    await agentGroupHeader.trigger('click')
    expect(mocks.push).toHaveBeenCalledWith({ path: '/rules', query: { agentId: 'edge-1', search: 'edge' } })
  })

  it('warns that results may be incomplete when some agent fetches fail', async () => {
    mocks.fetchAllAgentsRules.mockRejectedValue(new Error('agent unreachable'))
    mocks.fetchAllAgentsCertificates.mockResolvedValue([
      { agentId: 'edge-1', certificates: [{ id: 2, domain: 'edge.example.com' }] }
    ])
    const wrapper = mountSearch()
    await wrapper.get('input[name="global-search"]').setValue('example')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()

    expect(wrapper.text()).toContain('edge.example.com')
    expect(wrapper.get('.global-search-partial').text()).toContain('部分节点请求失败，结果可能不完整')
  })

  it('clears the partial-failure notice once every fetch succeeds again', async () => {
    mocks.fetchAllAgentsRules.mockRejectedValueOnce(new Error('agent unreachable'))
    const wrapper = mountSearch()
    await wrapper.get('input[name="global-search"]').setValue('example')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(wrapper.find('.global-search-partial').exists()).toBe(true)

    await wrapper.get('input[name="global-search"]').setValue('ample')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(wrapper.find('.global-search-partial').exists()).toBe(false)
  })
})
