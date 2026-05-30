import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import RuleDetailPage from './RuleDetailPage.vue'

let routeParams
let selectedAgentId
let rulesData
let updateRule

const routerBack = vi.fn()
const selectAgent = vi.fn()

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: routeParams }),
  useRouter: () => ({ back: routerBack })
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({
    selectedAgentId: { value: selectedAgentId },
    selectAgent
  })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: { value: [{ id: selectedAgentId, name: 'Edge' }] } })
}))

vi.mock('../hooks/useRules', () => ({
  useRules: () => ({ data: { value: rulesData } }),
  useCreateRule: () => ({ mutateAsync: vi.fn() }),
  useUpdateRule: () => ({ mutateAsync: updateRule })
}))

vi.mock('../components/QuickAgentSelect.vue', () => ({
  default: {
    name: 'QuickAgentSelect',
    props: ['agentId', 'agents'],
    emits: ['update:agentId'],
    template: '<div data-testid="agent-select"></div>'
  }
}))

describe('RuleDetailPage', () => {
  beforeEach(() => {
    routeParams = { id: '7' }
    selectedAgentId = 'edge-a'
    rulesData = [{
      id: 7,
      frontend_url: 'https://app.example.test',
      backends: [{ url: 'http://origin.example.test' }],
      relay_layers: [[77, 88]],
      tags: ['media'],
      enabled: true
    }]
    updateRule = vi.fn(async () => undefined)
    routerBack.mockReset()
    selectAgent.mockReset()
  })

  it('preserves existing relay layers when saving detail edits', async () => {
    const wrapper = mount(RuleDetailPage)
    await nextTick()

    const backendInput = wrapper.findAll('input')[1]
    await backendInput.setValue('http://origin-2.example.test')
    await wrapper.find('.btn-primary').trigger('click')

    expect(updateRule).toHaveBeenCalledTimes(1)
    expect(updateRule.mock.calls[0][0]).toMatchObject({
      id: 7,
      backends: [{ url: 'http://origin-2.example.test' }],
      relay_layers: [[77, 88]]
    })
  })
})
