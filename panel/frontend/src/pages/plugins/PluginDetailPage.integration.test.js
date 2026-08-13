import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDetailPage from './PluginDetailPage.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), fetchAgents: vi.fn(), refreshActor: vi.fn() }))
vi.mock('../../api/client', () => ({ api: { get: mocks.get, post: mocks.post } }))
vi.mock('../../api', () => ({ fetchAgents: mocks.fetchAgents }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'rpc.plugin' } }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('../../api/operations', () => ({ retryRevision: vi.fn() }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return { ...actual, useAccessControl: () => ({ actor: { value: { permissions: ['*'], visible_resource_groups: [] } }, can: () => true, refreshActor: mocks.refreshActor }) }
})
vi.mock('../../components/DeleteConfirmDialog.vue', () => ({
  default: {
    name: 'DeleteConfirmDialog',
    props: ['show', 'title', 'message', 'name', 'confirmText', 'loading'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" class="delete-dialog-stub"><button class="delete-dialog-confirm" @click="$emit(\'confirm\')">{{ confirmText }}</button></div>'
  }
}))

describe('PluginDetailPage production API projection', () => {
  it('keeps schema and handle metadata through the real API adapter', async () => {
    mocks.fetchAgents.mockResolvedValue([{ id: 'edge-a', name: 'Edge A', status: 'online' }])
    mocks.get.mockImplementation(async (path) => {
      if (path.endsWith('/operations')) return { data: { operations: [] } }
      return { data: {
        plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
        package: {
          manifest: { id: 'rpc.plugin', name: 'RPC Plugin' }, runtime: { kind: 'rpc-service' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] },
          config_schema: { type: 'object', required: ['credential'], properties: {
            token: { type: 'string', title: '普通 Token' },
            credential: { type: 'string', title: 'Credential', writeOnly: true, default: 'package-secret' },
            optional: { type: 'string', title: 'Optional', writeOnly: true }
          } }
        },
        instances: [{
          id: 'instance-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { token: 'active-value' }, secret_fields: [{ pointer: '/credential', present: false }], pending_operation_id: 'configure-pending',
          pending_config: { token: 'ordinary-value', credential: 'server-plaintext', optional: 'other-plaintext' },
          pending_secret_fields: [{ pointer: '/credential', present: true }, { pointer: '/optional', present: false }],
          config_version: 1, current_state: 'active'
        }, {
          id: 'instance-b', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { token: 'active-ordinary', credential: 'active-plaintext', optional: 'optional-plaintext' },
          secret_fields: [{ pointer: '/credential', present: false }, { pointer: '/optional', present: true }],
          config_version: 2, current_state: 'active'
        }],
        agent_statuses: []
      } }
    })
    mocks.post.mockResolvedValue({ data: { result: {} } })
    const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    expect(wrapper.text()).toContain('普通 Token')
    expect(wrapper.html()).not.toContain('server-plaintext')
    expect(wrapper.html()).not.toContain('other-plaintext')
    expect(wrapper.html()).not.toContain('package-secret')
    expect(wrapper.get('.declarative-field input[type="text"]').element.value).toBe('ordinary-value')
    expect(wrapper.get('.declarative-field input[type="password"]').attributes('required')).toBeUndefined()
    expect(wrapper.text()).toContain('已有凭据')
    expect(wrapper.findAll('button').filter((button) => button.text() === '清除凭据')).toHaveLength(0)

    await wrapper.findAll('button').find((button) => button.text() === '保存配置并生成 revision').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      config: { token: 'ordinary-value' }, secret_replacements: {}
    }))

    await wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes('instance-b')).trigger('click')
    await flushPromises()
    const activeSecretInputs = wrapper.findAll('.declarative-field input[type="password"]')
    expect(wrapper.get('.declarative-field input[type="text"]').element.value).toBe('active-ordinary')
    expect(wrapper.html()).not.toContain('active-plaintext')
    expect(wrapper.html()).not.toContain('optional-plaintext')
    expect(activeSecretInputs[0].attributes('required')).toBeDefined()
    expect(wrapper.findAll('button').filter((button) => button.text() === '清除凭据')).toHaveLength(1)
  })
})
