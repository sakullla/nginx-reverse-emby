import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDetailPage from './PluginDetailPage.vue'

const mocks = vi.hoisted(() => ({
  fetchPluginDetail: vi.fn(), fetchPluginOperations: vi.fn(), configurePlugin: vi.fn(),
  enablePlugin: vi.fn(), disablePlugin: vi.fn(), rollbackPlugin: vi.fn(), uninstallPlugin: vi.fn(),
  invokePluginDynamicAction: vi.fn(), fetchPluginLogs: vi.fn(), push: vi.fn(), refreshActor: vi.fn()
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'official.waf' } }), useRouter: () => ({ push: mocks.push }) }))
vi.mock('../../api/plugins', () => ({
  fetchPluginDetail: mocks.fetchPluginDetail, fetchPluginOperations: mocks.fetchPluginOperations, configurePlugin: mocks.configurePlugin,
  enablePlugin: mocks.enablePlugin, disablePlugin: mocks.disablePlugin, rollbackPlugin: mocks.rollbackPlugin, uninstallPlugin: mocks.uninstallPlugin,
  invokePluginDynamicAction: mocks.invokePluginDynamicAction, fetchPluginLogs: mocks.fetchPluginLogs
}))
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
    template: '<div v-if="show" class="delete-dialog-stub"><div class="delete-dialog-title">{{ title }}</div><button class="delete-dialog-confirm" @click="$emit(\'confirm\')">{{ confirmText }}</button><button class="delete-dialog-cancel" @click="$emit(\'cancel\')">取消</button></div>'
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

function submitButton(wrapper) {
  return wrapper.findAll('button').find((button) => button.text() === '保存配置并生成 revision')
}

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text() === text)
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
  mocks.push.mockReset()
  mocks.refreshActor.mockReset()
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
    const input = wrapper.get('.declarative-field input[type="text"]')
    await input.setValue('block')
    await submitButton(wrapper).trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', {
      instance_id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [],
      bindings: [{ consumer: { kind: 'http_rule', id: '1' }, target_agent_id: 'edge-a' }], config: { mode: 'block' }, secret_replacements: {}
    })
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
})
