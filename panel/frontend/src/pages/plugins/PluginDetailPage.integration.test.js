import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDetailPage from './PluginDetailPage.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), refreshActor: vi.fn() }))
vi.mock('../../api/client', () => ({ api: { get: mocks.get, post: mocks.post } }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'rpc.plugin' } }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('../../api/operations', () => ({ retryRevision: vi.fn() }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return { ...actual, useAccessControl: () => ({ actor: { value: { permissions: ['*'], visible_resource_groups: [] } }, can: () => true, refreshActor: mocks.refreshActor }) }
})

describe('PluginDetailPage production API projection', () => {
  it('keeps schema and handle metadata through the real API adapter', async () => {
    mocks.get.mockImplementation(async (path) => {
      if (path.endsWith('/operations')) return { data: { operations: [] } }
      return { data: {
        plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
        package: {
          manifest: { id: 'rpc.plugin', name: 'RPC Plugin' }, runtime: { kind: 'rpc-service' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] },
          config_schema: { type: 'object', required: ['credential'], properties: {
            token: { type: 'string', title: '普通 Token' },
            credential: { type: 'string', title: 'Credential', writeOnly: true, default: 'package-secret' }
          } }
        },
        instances: [{
          id: 'instance-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { token: 'active-value' }, secret_fields: [], pending_operation_id: 'configure-pending',
          pending_config: { token: 'ordinary-value', credential: 'server-plaintext' }, pending_secret_fields: ['/credential'],
          config_version: 1, current_state: 'active'
        }],
        agent_statuses: []
      } }
    })
    mocks.post.mockResolvedValue({ data: { result: {} } })
    const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    expect(wrapper.text()).toContain('普通 Token')
    expect(wrapper.html()).not.toContain('server-plaintext')
    expect(wrapper.html()).not.toContain('package-secret')
    expect(wrapper.get('.plugin-config-form input[type="text"]').element.value).toBe('ordinary-value')
    expect(wrapper.get('.plugin-config-form input[type="password"]').attributes('required')).toBeUndefined()
    expect(wrapper.text()).toContain('已有安全句柄')

    await wrapper.get('form.plugin-config-form').trigger('submit')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      config: { token: 'ordinary-value' }, secret_replacements: {}
    }))
  })
})
