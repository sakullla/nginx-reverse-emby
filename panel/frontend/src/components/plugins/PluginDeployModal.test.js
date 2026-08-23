import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDeployModal from './PluginDeployModal.vue'

const mocks = vi.hoisted(() => ({
  configurePlugin: vi.fn(),
  enablePlugin: vi.fn(),
  publishPlugin: vi.fn(),
  success: vi.fn(),
  error: vi.fn()
}))

vi.mock('../../api/plugins', () => ({
  configurePlugin: mocks.configurePlugin,
  enablePlugin: mocks.enablePlugin,
  publishPlugin: mocks.publishPlugin
}))

vi.mock('../../stores/messages', () => ({
  messageStore: {
    success: mocks.success,
    error: mocks.error
  }
}))

const BaseModalStub = {
  props: ['modelValue'],
  template: '<div v-if="modelValue"><slot /><slot name="footer" /></div>'
}

function mountModal(overrides = {}) {
  return mount(PluginDeployModal, {
    props: {
      modelValue: false,
      pluginId: 'official.plugin',
      document: { schema_version: 1, components: [] },
      formEmpty: true,
      resourceGroups: [{ id: 'default', name: '默认组' }],
      agents: [{ id: 'edge-a', name: 'Edge A' }, { id: 'edge-b', name: 'Edge B' }],
      currentLifecycle: 'active',
      ...overrides
    },
    global: {
      stubs: {
        BaseModal: BaseModalStub,
        PluginDeclarativeUI: true
      }
    }
  })
}

async function openModal(wrapper) {
  await wrapper.setProps({ modelValue: true })
  await flushPromises()
}

function submitButton(wrapper) {
  return wrapper.get('.plugin-deployment__empty-config .btn-primary')
}

describe('PluginDeployModal target authority', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.configurePlugin.mockResolvedValue({ id: 'official.plugin-default' })
  })

  it('fixes a control-plane-only deployment to the canonical local target', async () => {
    const wrapper = mountModal({
      targetEligibility: { canonical_local_target_id: 'local', agent_targets_allowed: false }
    })
    await openModal(wrapper)

    expect(wrapper.get('[data-test="plugin-deployment-local-target"]').text()).toContain('本地管理面')
    expect(wrapper.find('[data-test="deployment-agent"]').exists()).toBe(false)
    await submitButton(wrapper).trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.plugin', expect.objectContaining({
      targets: ['local']
    }))
    expect(mocks.configurePlugin.mock.calls[0][1].targets).not.toContain('edge-a')
  })

  it('keeps the Agent selector for a dual-face plugin', async () => {
    const wrapper = mountModal({
      targetEligibility: { canonical_local_target_id: 'local', agent_targets_allowed: true },
      faces: [{ face_id: 'local-management' }, { face_id: 'agent-execution' }]
    })
    await openModal(wrapper)

    expect(wrapper.find('[data-test="plugin-deployment-local-target"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="plugin-deployment-management-only"]').text()).toContain('不创建 Agent target')
    await submitButton(wrapper).trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.plugin', expect.objectContaining({ targets: [] }))

    mocks.configurePlugin.mockClear()
    await wrapper.get('[data-test="plugin-deployment-face-agent"]').trigger('click')
    expect(wrapper.get('[data-test="plugin-deployment-dual-face-note"]').text()).toContain('本地管理面自动固定')
    expect(wrapper.text()).toContain('Agent 执行面目标')
    const choices = wrapper.findAll('[data-test="deployment-agent"]')
    expect(choices).toHaveLength(2)
    await choices[0].setValue(true)
    await submitButton(wrapper).trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.plugin', expect.objectContaining({
      targets: ['edge-a']
    }))
  })

  it('fails closed when local target authority is incomplete', async () => {
    const wrapper = mountModal({
      targetEligibility: { canonical_local_target_id: '', agent_targets_allowed: false }
    })
    await openModal(wrapper)

    expect(wrapper.text()).toContain('本地管理面部署目标不可用')
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
  })

  it('keeps a direct target API error visible to the user', async () => {
    mocks.configurePlugin.mockRejectedValueOnce(new Error('invalid argument: plugin target is ineligible'))
    const wrapper = mountModal({
      targetEligibility: { canonical_local_target_id: 'local', agent_targets_allowed: false }
    })
    await openModal(wrapper)
    await submitButton(wrapper).trigger('click')
    await flushPromises()

    expect(mocks.error).toHaveBeenCalledWith('invalid argument: plugin target is ineligible')
  })
})
