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

function mountModal(targetEligibility) {
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
      canWrite: true,
      targetEligibility
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

  it('preserves selected Agent targets for a dual-face instance', async () => {
    const wrapper = mountModal({ canonical_local_target_id: 'local-control', agent_targets_allowed: true })
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
