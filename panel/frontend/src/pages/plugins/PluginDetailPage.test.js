import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDetailPage from './PluginDetailPage.vue'

const mocks = vi.hoisted(() => ({
  fetchPluginDetail: vi.fn(), fetchPluginOperations: vi.fn(), configurePlugin: vi.fn(),
  enablePlugin: vi.fn(), disablePlugin: vi.fn(), rollbackPlugin: vi.fn(), uninstallPlugin: vi.fn(),
  invokePluginDynamicAction: vi.fn(), fetchPluginLogs: vi.fn(), fetchAgents: vi.fn(),
  fetchResourceGroups: vi.fn(), push: vi.fn(), refreshActor: vi.fn(),
  actor: { permissions: ['*'], visible_resource_groups: [] }
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'official.waf' } }), useRouter: () => ({ push: mocks.push }) }))
vi.mock('../../api', () => ({ fetchAgents: mocks.fetchAgents }))
vi.mock('../../api/access', () => ({ fetchResourceGroups: mocks.fetchResourceGroups }))
vi.mock('../../api/plugins', () => ({
  fetchPluginDetail: mocks.fetchPluginDetail, fetchPluginOperations: mocks.fetchPluginOperations, configurePlugin: mocks.configurePlugin,
  enablePlugin: mocks.enablePlugin, disablePlugin: mocks.disablePlugin, rollbackPlugin: mocks.rollbackPlugin, uninstallPlugin: mocks.uninstallPlugin,
  invokePluginDynamicAction: mocks.invokePluginDynamicAction, fetchPluginLogs: mocks.fetchPluginLogs
}))
vi.mock('../../api/operations', () => ({ retryRevision: vi.fn() }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      actor: { value: mocks.actor },
      can: (permission) => (mocks.actor.permissions || []).includes('*') || (mocks.actor.permissions || []).includes(permission),
      refreshActor: mocks.refreshActor
    })
  }
})
vi.mock('../../components/DeleteConfirmDialog.vue', () => ({
  default: {
    name: 'DeleteConfirmDialog',
    props: ['show', 'title', 'message', 'name', 'confirmText', 'loading'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" class="delete-dialog-stub"><div class="delete-dialog-title">{{ title }}</div><button class="delete-dialog-confirm" @click="$emit(\'confirm\')">{{ confirmText }}</button><button class="delete-dialog-cancel" @click="$emit(\'cancel\')">取消</button></div>'
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

function makeDetail(overrides = {}) {
  return {
    plugin: { plugin_id: 'official.waf', current_lifecycle: 'active', state_version: 2, active_source_kind: 'official', active_source_risk_label: 'official' },
    package: { version: '1.0.0', manifest: { id: 'official.waf', name: 'WAF' }, runtime: { kind: 'wasm-policy', abi: 'nre:policy/v1' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] }, config_schema: { type: 'object', properties: { mode: { type: 'string', title: '模式' } } } },
    instances: [{ id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [{ consumer: { kind: 'http_rule', id: '1', resource_group_id: 'group-a', version: 'a'.repeat(64) }, target_agent_id: 'edge-a' }], config: { mode: 'observe' }, config_version: 1, current_state: 'active' }],
    agent_statuses: [],
    ...overrides
  }
}

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text() === text)
}

function deployModal(wrapper) {
  return wrapper.find('[data-test="plugin-deploy-modal"]')
}

function configModal(wrapper) {
  return wrapper.find('[data-test="plugin-instance-config-modal"]')
}

function modalButton(modal, text) {
  return modal.findAll('button').find((button) => button.text() === text)
}

async function openDeployModal(wrapper) {
  await buttonByText(wrapper, '部署').trigger('click')
  return wrapper.get('[data-test="plugin-deploy-modal"]')
}

async function openConfigModal(wrapper) {
  await buttonByText(wrapper, '编辑配置').trigger('click')
  return wrapper.get('[data-test="plugin-instance-config-modal"]')
}

beforeEach(() => {
  mocks.fetchPluginDetail.mockReset().mockResolvedValue(makeDetail())
  mocks.fetchPluginOperations.mockReset().mockResolvedValue([{ id: 'op', kind: 'configure', status: 'failed', error: 'token=raw-token', agent_results: {} }])
  mocks.configurePlugin.mockReset().mockResolvedValue({})
  mocks.enablePlugin.mockReset().mockResolvedValue({})
  mocks.disablePlugin.mockReset().mockResolvedValue({})
  mocks.rollbackPlugin.mockReset().mockResolvedValue({})
  mocks.uninstallPlugin.mockReset().mockResolvedValue({})
  mocks.invokePluginDynamicAction.mockReset()
  mocks.fetchPluginLogs.mockReset().mockResolvedValue({ entries: [], next_cursor: '' })
  mocks.fetchAgents.mockReset().mockResolvedValue([
    { id: 'edge-a', name: 'Edge A', status: 'online', desired_revision: 1, current_revision: 1, last_apply_status: 'success' },
    { id: 'edge-b', name: 'Edge B', status: 'online', desired_revision: 2, current_revision: 2, last_apply_status: 'success' }
  ])
  mocks.fetchResourceGroups.mockReset().mockResolvedValue([
    { id: 'default', name: '默认组' },
    { id: 'team', name: '团队组' }
  ])
  mocks.push.mockReset()
  mocks.refreshActor.mockReset()
  mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
})

async function mountPage(detail = makeDetail()) {
  mocks.fetchPluginDetail.mockResolvedValue(detail)
  const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}

describe('PluginDetailPage', () => {
  it('renders the shared page header with a primary action and a danger uninstall', async () => {
    const wrapper = await mountPage()
    expect(wrapper.find('.page-header').exists()).toBe(true)
    expect(wrapper.find('.page-title').text()).toBe('WAF')
    expect(buttonByText(wrapper, '启用').classes()).toContain('btn-primary')
    expect(buttonByText(wrapper, '停用').classes()).toContain('btn-secondary')
    expect(buttonByText(wrapper, '卸载').classes()).toContain('btn-danger')
  })

  it('switches instances with BaseTabs instead of a native select', async () => {
    const detail = makeDetail()
    detail.instances = [
      { id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [], config: { mode: 'observe' }, config_version: 1, current_state: 'active' },
      { id: 'waf-b', resource_group_id: 'group-b', targets: ['edge-b'], policy_chains: [], bindings: [], config: { mode: 'block' }, config_version: 2, current_state: 'active' }
    ]
    const wrapper = await mountPage(detail)
    const tablist = wrapper.find('[role="tablist"]')
    expect(tablist.exists()).toBe(true)
    expect(tablist.text()).toContain('waf-a · group-a')
    expect(tablist.text()).toContain('waf-b · group-b')
    expect(wrapper.find('select option[value="waf-a"]').exists()).toBe(false)
  })

  it('submits host-schema config with caller-owned binding fields and redacts errors', async () => {
    const wrapper = await mountPage()
    expect(wrapper.text()).not.toContain('raw-token')
    expect(configModal(wrapper).exists()).toBe(false)
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', {
      instance_id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [],
      bindings: [{ consumer: { kind: 'http_rule', id: '1' }, target_agent_id: 'edge-a' }], config: { mode: 'block' }, secret_replacements: {}
    })
  })

  it('creates, configures, and enables a new instance for multiple selected agents', async () => {
    const detail = makeDetail({
      plugin: { ...makeDetail().plugin, current_lifecycle: 'disabled', desired_lifecycle: 'disabled' },
      instances: []
    })
    mocks.configurePlugin.mockResolvedValue({ id: 'official.waf-default' })
    const wrapper = await mountPage(detail)

    expect(deployModal(wrapper).exists()).toBe(false)
    const modal = await openDeployModal(wrapper)
    expect(modal.find('[aria-label="部署插件实例"]').exists()).toBe(true)
    expect(modal.find('[data-test="deployment-instance-id"]').exists()).toBe(false)
    const groupSelect = modal.get('[data-test="deployment-resource-group"]')
    expect(groupSelect.element.tagName).toBe('SELECT')
    expect(groupSelect.element.value).toBe('default')
    const targetInputs = modal.findAll('.plugin-deployment__agent input[type="checkbox"]')
    await targetInputs[0].setValue(true)
    await targetInputs[1].setValue(true)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '部署').trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', {
      instance_id: 'official.waf-default',
      resource_group_id: 'default',
      targets: ['edge-a', 'edge-b'],
      policy_chains: [],
      bindings: [],
      config: { mode: 'block' },
      secret_replacements: {}
    })
    expect(mocks.enablePlugin).toHaveBeenCalledWith('official.waf')
    expect(deployModal(wrapper).exists()).toBe(false)
  })

  it('confirms uninstall through DeleteConfirmDialog and navigates away', async () => {
    const detail = makeDetail()
    detail.plugin.current_lifecycle = 'disabled'
    const wrapper = await mountPage(detail)
    await buttonByText(wrapper, '卸载').trigger('click')
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.uninstallPlugin).toHaveBeenCalledWith('official.waf')
    expect(mocks.push).toHaveBeenCalledWith('/plugins')
  })

  it('enables immediately but gates disable behind DeleteConfirmDialog', async () => {
    const wrapper = await mountPage()

    await buttonByText(wrapper, '启用').trigger('click')
    await flushPromises()
    expect(mocks.enablePlugin).toHaveBeenCalledWith('official.waf')

    await buttonByText(wrapper, '停用').trigger('click')
    expect(mocks.disablePlugin).not.toHaveBeenCalled()
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)
    expect(wrapper.find('.delete-dialog-title').text()).toBe('确认停用插件')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.disablePlugin).toHaveBeenCalledWith('official.waf')
  })

  it('requires confirmation before rolling back', async () => {
    const detail = makeDetail()
    detail.plugin.rollback_package_digest = 'sha256:rollback-digest'
    const wrapper = await mountPage(detail)

    await buttonByText(wrapper, '回滚').trigger('click')
    expect(mocks.rollbackPlugin).not.toHaveBeenCalled()
    expect(wrapper.find('.delete-dialog-title').text()).toBe('确认回滚插件')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.rollbackPlugin).toHaveBeenCalledWith('official.waf', [])
  })

  it('submits the selected visible resource group and does not require an instance id field', async () => {
    const detail = makeDetail({
      plugin: { ...makeDetail().plugin, current_lifecycle: 'disabled', desired_lifecycle: 'disabled' },
      instances: []
    })
    mocks.configurePlugin.mockResolvedValue({ id: 'official.waf-default' })
    const wrapper = await mountPage(detail)
    const modal = await openDeployModal(wrapper)
    await modal.get('[data-test="deployment-resource-group"]').setValue('team')
    const targetInputs = modal.findAll('.plugin-deployment__agent input[type="checkbox"]')
    await targetInputs[0].setValue(true)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '部署').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      instance_id: 'official.waf-default',
      resource_group_id: 'team',
      targets: ['edge-a'],
      policy_chains: [],
      bindings: []
    }))
  })

  it('keeps an installed plugin with no visible instances and opens the deploy modal on demand', async () => {
    const wrapper = await mountPage(makeDetail({ instances: [], agent_statuses: [] }))
    expect(wrapper.text()).toContain('尚未部署')
    expect(deployModal(wrapper).exists()).toBe(false)
    expect(wrapper.find('.plugin-technical').exists()).toBe(true)
    expect(wrapper.find('.plugin-technical').element.open).toBeFalsy()
    const modal = await openDeployModal(wrapper)
    expect(modal.find('[aria-label="部署插件实例"]').exists()).toBe(true)
  })

  it('shows only instances from groups the current actor can see', async () => {
    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['group-a'] }
    const detail = makeDetail()
    detail.instances = [
      { id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [], config: { mode: 'observe' }, config_version: 1, current_state: 'active' },
      { id: 'waf-b', resource_group_id: 'group-b', targets: ['edge-b'], policy_chains: [], bindings: [], config: { mode: 'block' }, config_version: 2, current_state: 'active' }
    ]
    const wrapper = await mountPage(detail)
    expect(wrapper.text()).toContain('waf-a · group-a')
    expect(wrapper.text()).not.toContain('waf-b · group-b')
    expect(wrapper.find('input[data-test="deployment-resource-group"]').exists()).toBe(false)
  })

  it('blocks deploy when no visible resource group or agent is selected', async () => {
    mocks.fetchResourceGroups.mockResolvedValue([])
    mocks.fetchAgents.mockResolvedValue([])
    const wrapper = await mountPage(makeDetail({ instances: [] }))
    const modal = await openDeployModal(wrapper)
    expect(modal.text()).toContain('当前身份没有可见的资源组，无法部署。')
    expect(modalButton(modal, '部署').attributes('disabled')).toBeDefined()
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
  })

  it('lets a resource writer save a schema fallback form without declarative UI', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['group-a'] }
    const wrapper = await mountPage()
    expect(wrapper.text()).not.toContain('当前身份只有只读权限')
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      instance_id: 'waf-a',
      config: { mode: 'block' }
    }))
  })

  it('lets a resource writer save a plugin that already has declarative UI', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['group-a'] }
    const detail = makeDetail({
      package: {
        ...makeDetail().package,
        declarative_ui: {
          schema_version: 1,
          title: 'WAF',
          components: [{ type: 'text', id: 'mode', label: '模式', binding: '/mode' }],
          actions: [{ type: 'submit', id: 'save', label: '保存配置' }]
        }
      }
    })
    const wrapper = await mountPage(detail)
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      config: { mode: 'block' }
    }))
  })

  it('keeps configuration closed for members without write permission', async () => {
    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['group-a'] }
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('当前身份只有只读权限')
    expect(buttonByText(wrapper, '编辑配置')).toBeUndefined()
    expect(configModal(wrapper).exists()).toBe(false)
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
  })

  it('does not call configurePlugin when visible schema validation fails', async () => {
    const detail = makeDetail({
      package: {
        ...makeDetail().package,
        config_schema: {
          type: 'object',
          required: ['mode', 'port'],
          properties: {
            mode: { type: 'string', title: '模式', minLength: 2, pattern: '^[a-z]+$' },
            port: { type: 'number', title: '端口', minimum: 1, maximum: 65535 },
            sources: {
              type: 'array',
              items: {
                type: 'object',
                required: ['host'],
                properties: { host: { type: 'string', title: '主机', minLength: 2 } }
              }
            }
          }
        }
      },
      instances: [{
        ...makeDetail().instances[0],
        config: { mode: 'X', port: 0, sources: [{ host: 'a' }] }
      }]
    })
    const wrapper = await mountPage(detail)
    const modal = await openConfigModal(wrapper)
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    expect(modal.text()).toMatch(/至少 2 个字符|格式不匹配/)
    expect(modal.text()).toContain('不能小于 1')
  })

  it('saves nested objects and arrays from the schema fallback form', async () => {
    const detail = makeDetail({
      package: {
        ...makeDetail().package,
        config_schema: {
          type: 'object',
          properties: {
            credentials: { type: 'object', title: '凭据', properties: { region: { type: 'string', title: '区域' } } },
            sources: {
              type: 'array',
              title: '源',
              items: { type: 'object', properties: { host: { type: 'string', title: '主机' } } }
            }
          }
        }
      },
      instances: [{
        ...makeDetail().instances[0],
        config: { credentials: { region: 'us' }, sources: [{ host: 'a.example' }] }
      }]
    })
    const wrapper = await mountPage(detail)
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-section input[type="text"]').setValue('eu')
    await modal.findAll('button').find((button) => button.text() === '+ 添加').trigger('click')
    const itemInputs = modal.findAll('.declarative-array-item input[type="text"]')
    await itemInputs[1].setValue('b.example')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      config: { credentials: { region: 'eu' }, sources: [{ host: 'a.example' }, { host: 'b.example' }] }
    }))
  })
})
