import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

let diagnoseRuleResponse
let diagnoseL4RuleResponse

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: { agentId: 'edge-a' } }),
  useRouter: () => ({ replace: vi.fn() })
}))

vi.mock('../context/AgentContext', () => ({
  useAgent: () => ({ selectedAgentId: { value: 'edge-a' } })
}))

vi.mock('../hooks/useAgents', () => ({
  useAgents: () => ({ data: { value: [{ id: 'edge-a' }] } })
}))

vi.mock('../hooks/useRules', () => ({
  useRules: () => ({ data: { value: [{ id: 7, frontend_url: 'https://app.example.test', backend_url: 'http://origin.example.test', enabled: true }] }, isLoading: { value: false } }),
  useCreateRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useUpdateRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useDeleteRule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() })
}))

vi.mock('../hooks/useL4Rules', () => ({
  useL4Rules: () => ({ data: { value: [{ id: 9, name: 'tcp-app', listen_host: '0.0.0.0', listen_port: 443, protocol: 'tcp', backends: [{ address: '10.0.0.1:443' }], enabled: true }] }, isLoading: { value: false } }),
  useCreateL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useUpdateL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useDeleteL4Rule: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() })
}))

vi.mock('../hooks/useDiagnostics', () => ({
  useDiagnosticTask: () => ({ data: { value: null } }),
  useDiagnoseRule: () => ({ mutateAsync: vi.fn(() => Promise.resolve(diagnoseRuleResponse)) }),
  useDiagnoseL4Rule: () => ({ mutateAsync: vi.fn(() => Promise.resolve(diagnoseL4RuleResponse)) })
}))

const modalStub = {
  name: 'RuleDiagnosticModal',
  props: ['modelValue', 'task', 'kind'],
  template: '<div data-testid="diagnostic-modal" :data-kind="kind" :data-task-id="task?.id || \'\'">{{ task?.state || \'empty\' }}</div>'
}

const commonStubs = {
  RuleForm: true,
  L4RuleForm: true,
  DeleteConfirmDialog: true,
  BaseModal: true,
  RuleDiagnosticModal: modalStub,
  RouterLink: true,
  AgentPicker: true
}

beforeEach(() => {
  diagnoseRuleResponse = {
    ok: true,
    task_id: 'task-http-1',
    task: { id: 'task-http-1', state: 'pending', result: { summary: { avg_latency_ms: 11 } } }
  }
  diagnoseL4RuleResponse = {
    ok: true,
    task_id: 'task-l4-1',
    task: { id: 'task-l4-1', state: 'pending', result: { summary: { avg_latency_ms: 12 } } }
  }
})

describe('diagnostic pages initial task echo', () => {
  it('passes HTTP diagnose response task to the modal immediately', async () => {
    const { default: RulesPage } = await import('./RulesPage.vue')
    const wrapper = mount(RulesPage, { global: { stubs: commonStubs } })

    await wrapper.get('.rule-card__action--diagnose').trigger('click')
    await nextTick()

    expect(wrapper.get('[data-testid="diagnostic-modal"]').attributes('data-task-id')).toBe('task-http-1')
    expect(wrapper.get('[data-testid="diagnostic-modal"]').text()).toContain('pending')
  })

  it('passes L4 diagnose response task to the modal immediately', async () => {
    const { default: L4RulesPage } = await import('./L4RulesPage.vue')
    const wrapper = mount(L4RulesPage, { global: { stubs: commonStubs } })

    await wrapper.get('.l4-card__action--diagnose').trigger('click')
    await nextTick()

    expect(wrapper.get('[data-testid="diagnostic-modal"]').attributes('data-task-id')).toBe('task-l4-1')
    expect(wrapper.get('[data-testid="diagnostic-modal"]').text()).toContain('pending')
  })
})
