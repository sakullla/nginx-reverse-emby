import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginInstanceConfigModal from './PluginInstanceConfigModal.vue'

const mocks = vi.hoisted(() => ({
  configurePlugin: vi.fn(),
  invokePluginDynamicAction: vi.fn(),
  publishPlugin: vi.fn(),
  error: vi.fn(),
  success: vi.fn()
}))

vi.mock('../../api', () => ({
  fetchAllAgentsRules: vi.fn(),
  fetchHttpRulesPage: vi.fn()
}))

vi.mock('../../api/plugins', () => ({
  configurePlugin: mocks.configurePlugin,
  invokePluginDynamicAction: mocks.invokePluginDynamicAction,
  publishPlugin: mocks.publishPlugin
}))

vi.mock('../../stores/messages', () => ({
  messageStore: {
    error: mocks.error,
    success: mocks.success
  }
}))

const BaseModalStub = {
  props: ['modelValue'],
  template: '<div v-if="modelValue"><slot /><slot name="footer" /></div>'
}

const PluginDeclarativeUIStub = {
  emits: ['submit'],
  template: '<button data-test="save-config" @click="$emit(\'submit\', { config: { mode: \'observe\' }, secret_replacements: {} })">保存</button>'
}

function mountModal(targetEligibility, overrides = {}) {
  return mount(PluginInstanceConfigModal, {
    props: {
      modelValue: true,
      pluginId: 'official.plugin',
      instance: {
        id: 'official.plugin-default',
        resource_group_id: 'default',
        targets: ['edge-a'],
        policy_chains: [],
        bindings: []
      },
      document: { schema_version: 1, components: [] },
      configSchema: { type: 'object', properties: { mode: { type: 'string' } } },
      packageDetail: { manifest: { extension_points: [] } },
      agents: [{ id: 'edge-a', name: 'Edge A', status: 'online' }, { id: 'edge-b', name: 'Edge B', status: 'online' }],
      canWrite: true,
      targetEligibility,
      ...overrides
    },
    global: {
      stubs: {
        BaseModal: BaseModalStub,
        PluginDeclarativeUI: PluginDeclarativeUIStub
      }
    }
  })
}

describe('PluginInstanceConfigModal target authority', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.configurePlugin.mockResolvedValue({ id: 'official.plugin-default' })
  })

  it('saves a control-plane-only instance against the canonical local target', async () => {
    const wrapper = mountModal({ canonical_local_target_id: 'local-control', agent_targets_allowed: false })
    expect(wrapper.get('[data-test="plugin-config-local-target"]').text()).toContain('local-control')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.plugin', expect.objectContaining({
      targets: ['local-control']
    }))
    expect(mocks.configurePlugin.mock.calls[0][1].targets).not.toContain('edge-a')
  })

  it('lets a dual-face instance change only its Agent execution targets', async () => {
    const wrapper = mountModal(
      { canonical_local_target_id: 'local-control', agent_targets_allowed: true },
      { faces: [{ face_id: 'local-management' }, { face_id: 'agent-execution' }] }
    )
    await wrapper.get('[data-test="plugin-config-face-agent"]').trigger('click')
    expect(wrapper.get('[data-test="plugin-config-dual-face-note"]').text()).toContain('本地管理面自动固定')
    const targets = wrapper.findAll('[data-test="plugin-config-agent-target"]')
    expect(targets).toHaveLength(2)
    expect(targets[0].element.checked).toBe(true)
    await targets[0].setValue(false)
    await targets[1].setValue(true)
    await wrapper.get('[data-test="plugin-save-agent-face"]').trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.plugin', expect.objectContaining({
      targets: ['edge-b']
    }))
  })

  it('allows a dual-face instance to keep the Agent execution face undeployed', async () => {
    const wrapper = mountModal(
      { canonical_local_target_id: 'local-control', agent_targets_allowed: true },
      {
        faces: [{ face_id: 'local-management' }, { face_id: 'agent-execution' }],
        instance: { id: 'official.plugin-default', resource_group_id: 'default', targets: [], policy_chains: [], bindings: [] }
      }
    )
    await wrapper.get('[data-test="plugin-config-face-agent"]').trigger('click')
    await wrapper.get('[data-test="plugin-save-agent-face"]').trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.plugin', expect.objectContaining({
      targets: []
    }))
  })

  it('keeps an HTTP-backed instance target fixed while editing configuration', async () => {
    const wrapper = mountModal(
      { canonical_local_target_id: 'local-control', agent_targets_allowed: true },
      {
        hasHTTPBackend: true,
        faces: [{ face_id: 'local-management' }, { face_id: 'agent-execution' }],
        packageDetail: { manifest: { extension_points: [], http_backend_providers: [{ id: 'default' }] } },
        publishedEntries: [{ rule_id: 1, agent_id: 'edge-a', frontend_url: 'https://example.test', enabled: true }]
      }
    )
    expect(wrapper.find('[data-test="plugin-config-face-switch"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="plugin-config-agent-targets"]').exists()).toBe(false)

    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.plugin', expect.objectContaining({
      targets: ['edge-a']
    }))
  })

  it('blocks a local save when the canonical target is missing', async () => {
    const wrapper = mountModal({ canonical_local_target_id: '', agent_targets_allowed: false })
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalledWith('本地管理面部署目标不可用，请刷新详情后重试。')
  })
})
