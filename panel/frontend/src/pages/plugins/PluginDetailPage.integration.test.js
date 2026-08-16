import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDetailPage from './PluginDetailPage.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  fetchAgents: vi.fn(),
  refreshActor: vi.fn(),
  actor: { permissions: ['*'], visible_resource_groups: [] }
}))
vi.mock('../../api/client', () => ({ api: { get: mocks.get, post: mocks.post }, longRunningRequest: { timeout: 0 } }))
vi.mock('../../api', () => ({ fetchAgents: mocks.fetchAgents }))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'rpc.plugin' } }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('../../api/operations', () => ({ retryRevision: vi.fn() }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      actor: { value: mocks.actor },
      can: (permission) => mocks.actor.permissions.includes('*') || mocks.actor.permissions.includes(permission),
      refreshActor: mocks.refreshActor
    })
  }
})
vi.mock('../../components/DeleteConfirmDialog.vue', () => ({
  default: {
    name: 'DeleteConfirmDialog',
    props: ['show', 'title', 'message', 'name', 'confirmText', 'loading'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" class="delete-dialog-stub"><button class="delete-dialog-confirm" @click="$emit(\'confirm\')">{{ confirmText }}</button></div>'
  }
}))
vi.mock('../../components/base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: ['modelValue', 'title', 'subtitle', 'size', 'showFooter', 'closeOnClickModal', 'dataTest'],
    emits: ['update:modelValue', 'confirm'],
    template: '<div v-if="modelValue" class="base-modal-stub" :data-test="dataTest"><slot /><slot name="footer" /></div>'
  }
}))

function configModal(wrapper) {
  return wrapper.find('[data-test="plugin-instance-config-modal"]')
}

async function openConfigModal(wrapper) {
  await wrapper.findAll('button').find((button) => button.text() === '编辑配置').trigger('click')
  return wrapper.get('[data-test="plugin-instance-config-modal"]')
}

describe('PluginDetailPage production API projection', () => {
  beforeEach(() => {
    mocks.get.mockReset()
    mocks.post.mockReset()
    mocks.fetchAgents.mockReset()
    mocks.refreshActor.mockReset()
    mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
  })

  it('keeps schema and handle metadata through the real API adapter', async () => {
    mocks.fetchAgents.mockResolvedValue([{ id: 'edge-a', name: 'Edge A', status: 'online' }])
    mocks.get.mockImplementation(async (path) => {
      if (path.endsWith('/operations')) return { data: { operations: [] } }
      if (path.includes('/access/resource-groups')) return { data: { resource_groups: [{ id: 'default', name: '默认组' }, { id: 'group-a', name: '组 A' }] } }
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

    expect(wrapper.text()).not.toContain('普通 Token')
    expect(wrapper.html()).not.toContain('server-plaintext')
    expect(wrapper.html()).not.toContain('other-plaintext')
    expect(wrapper.html()).not.toContain('package-secret')
    const pendingModal = await openConfigModal(wrapper)
    expect(pendingModal.text()).toContain('普通 Token')
    expect(pendingModal.get('.declarative-field input[type="text"]').element.value).toBe('ordinary-value')
    expect(pendingModal.get('.declarative-field input[type="password"]').attributes('required')).toBeUndefined()
    expect(pendingModal.text()).toContain('已有凭据')
    expect(pendingModal.findAll('button').filter((button) => button.text() === '清除凭据')).toHaveLength(0)

    await pendingModal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      config: { token: 'ordinary-value' }, secret_replacements: {}
    }), { timeout: 0 })
    expect(configModal(wrapper).exists()).toBe(false)

    await wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes('instance-b')).trigger('click')
    await flushPromises()
    const activeModal = await openConfigModal(wrapper)
    const activeSecretInputs = activeModal.findAll('.declarative-field input[type="password"]')
    expect(activeModal.get('.declarative-field input[type="text"]').element.value).toBe('active-ordinary')
    expect(activeModal.html()).not.toContain('active-plaintext')
    expect(activeModal.html()).not.toContain('optional-plaintext')
    expect(activeSecretInputs[0].attributes('required')).toBeDefined()
    expect(activeModal.findAll('button').filter((button) => button.text() === '清除凭据')).toHaveLength(1)
  })

  it('saves a nested schema fallback form and blocks invalid configure posts', async () => {
    mocks.fetchAgents.mockResolvedValue([{ id: 'edge-a', name: 'Edge A', status: 'online' }])
    mocks.get.mockImplementation(async (path) => {
      if (path.endsWith('/operations')) return { data: { operations: [] } }
      if (path.includes('/access/resource-groups')) return { data: { resource_groups: [{ id: 'default', name: '默认组' }] } }
      return { data: {
        plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
        package: {
          manifest: { id: 'rpc.plugin', name: 'RPC Plugin' }, runtime: { kind: 'rpc-service' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] },
          config_schema: {
            type: 'object',
            required: ['region'],
            properties: {
              region: { type: 'string', title: '区域', minLength: 2 },
              sources: {
                type: 'array',
                title: '源',
                items: { type: 'object', required: ['host'], properties: { host: { type: 'string', title: '主机', minLength: 2 } } }
              }
            }
          }
        },
        instances: [{
          id: 'instance-a', resource_group_id: 'default', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { region: 'X', sources: [{ host: 'a' }] },
          config_version: 1, current_state: 'active'
        }],
        agent_statuses: []
      } }
    })
    mocks.post.mockResolvedValue({ data: { result: {} } })
    const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    const modal = await openConfigModal(wrapper)
    await modal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(modal.text()).toContain('至少 2 个字符')

    const inputs = modal.findAll('.declarative-field input[type="text"]')
    await inputs[0].setValue('eu')
    await inputs[1].setValue('edge.example')
    await modal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      config: { region: 'eu', sources: [{ host: 'edge.example' }] }
    }), { timeout: 0 })
  })

  it('lets a resource writer persist an existing instance through the real configure adapter', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['group-a'] }
    mocks.fetchAgents.mockResolvedValue([{ id: 'edge-a', name: 'Edge A', status: 'online' }])
    mocks.get.mockImplementation(async (path) => {
      if (path.endsWith('/operations')) return { data: { operations: [] } }
      if (path.includes('/access/resource-groups')) return { data: { resource_groups: [{ id: 'group-a', name: '组 A' }] } }
      return { data: {
        plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
        package: {
          manifest: { id: 'rpc.plugin', name: 'RPC Plugin' }, runtime: { kind: 'rpc-service' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] },
          config_schema: { type: 'object', required: ['mode'], properties: { mode: { type: 'string', title: '模式' } } }
        },
        instances: [{
          id: 'instance-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { mode: 'observe' }, config_version: 1, current_state: 'active'
        }],
        agent_statuses: []
      } }
    })
    mocks.post.mockResolvedValue({ data: { result: {} } })
    const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      instance_id: 'instance-a',
      resource_group_id: 'group-a',
      config: { mode: 'block' }
    }), { timeout: 0 })
  })
})
