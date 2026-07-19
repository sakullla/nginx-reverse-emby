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
  return mount(GlobalSearch, {
    props: { open: true },
    attachTo: document.body,
    global: {
      stubs: {
        Teleport: true
      }
    }
  })
}

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
