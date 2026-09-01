import { mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AgentProvider } from './AgentContext.js'
import { clearCredentials, setAuthToken } from '../api/authState'

const { fetchSystemInfo } = vi.hoisted(() => ({ fetchSystemInfo: vi.fn() }))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: ref([]) })
}))

vi.mock('../api', () => ({ fetchSystemInfo }))

describe('AgentProvider session boundary', () => {
  beforeEach(() => {
    clearCredentials()
    localStorage.clear()
    fetchSystemInfo.mockReset()
    fetchSystemInfo.mockResolvedValue({ default_agent_id: 'edge-a' })
  })

  it('loads system info for a fresh panel token and clears selection on identity replacement', async () => {
    localStorage.setItem('selected_agent_id', 'hidden-agent')
    setAuthToken('panel-token')
    mount(AgentProvider, { slots: { default: defineComponent({ template: '<div />' }) } })
    await nextTick()
    await Promise.resolve()

    expect(fetchSystemInfo).toHaveBeenCalledOnce()

    setAuthToken('replacement-token')
    await nextTick()
    expect(localStorage.getItem('selected_agent_id')).toBeNull()
  })
})
