import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDetailPage from './PluginDetailPage.vue'

const mocks = vi.hoisted(() => ({
  fetchPluginDetail: vi.fn(), fetchPluginOperations: vi.fn(), configurePlugin: vi.fn(),
  enablePlugin: vi.fn(), disablePlugin: vi.fn(), rollbackPlugin: vi.fn(), uninstallPlugin: vi.fn(), push: vi.fn(), refreshActor: vi.fn()
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'official.waf' } }), useRouter: () => ({ push: mocks.push }) }))
vi.mock('../../api/plugins', () => ({
  fetchPluginDetail: mocks.fetchPluginDetail, fetchPluginOperations: mocks.fetchPluginOperations, configurePlugin: mocks.configurePlugin,
  enablePlugin: mocks.enablePlugin, disablePlugin: mocks.disablePlugin, rollbackPlugin: mocks.rollbackPlugin, uninstallPlugin: mocks.uninstallPlugin
}))
vi.mock('../../api/operations', () => ({ retryRevision: vi.fn() }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return { ...actual, useAccessControl: () => ({ actor: { value: { permissions: ['*'], visible_resource_groups: [] } }, can: () => true, refreshActor: mocks.refreshActor }) }
})

const detail = {
  plugin: { plugin_id: 'official.waf', current_lifecycle: 'active', state_version: 2, active_source_kind: 'official', active_source_risk_label: 'official' },
  package: { version: '1.0.0', manifest: { id: 'official.waf', name: 'WAF' }, runtime: { kind: 'wasm-policy', abi: 'nre:policy/v1' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] }, config_schema: { type: 'object', properties: { mode: { type: 'string', title: '模式' } } } },
  instances: [{ id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [{ consumer: { kind: 'http_rule', id: '1', resource_group_id: 'group-a', version: 'a'.repeat(64) }, target_agent_id: 'edge-a' }], config: { mode: 'observe' }, config_version: 1, current_state: 'active' }],
  agent_statuses: []
}

beforeEach(() => {
  mocks.fetchPluginDetail.mockReset().mockResolvedValue(detail)
  mocks.fetchPluginOperations.mockReset().mockResolvedValue([{ id: 'op', kind: 'configure', status: 'failed', error: 'token=raw-token', agent_results: {} }])
  mocks.configurePlugin.mockReset().mockResolvedValue({})
  mocks.refreshActor.mockReset()
})

describe('PluginDetailPage', () => {
  it('submits host-schema config with caller-owned binding fields and redacts errors', async () => {
    const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('raw-token')
    const input = wrapper.get('.plugin-config-form input[type="text"]')
    await input.setValue('block')
    await wrapper.get('form.plugin-config-form').trigger('submit')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', {
      instance_id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [],
      bindings: [{ consumer: { kind: 'http_rule', id: '1' }, target_agent_id: 'edge-a' }], config: { mode: 'block' }
    })
  })
})
