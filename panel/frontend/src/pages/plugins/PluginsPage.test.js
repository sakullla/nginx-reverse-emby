import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginsPage from './PluginsPage.vue'

const mocks = vi.hoisted(() => ({ fetchPlugins: vi.fn(), fetchPluginDetail: vi.fn(), refreshActor: vi.fn() }))
vi.mock('../../api/plugins', () => ({ fetchPlugins: mocks.fetchPlugins, fetchPluginDetail: mocks.fetchPluginDetail }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return { ...actual, useAccessControl: () => ({ actor: { value: { permissions: ['resource.read'], visible_resource_groups: ['group-a'] } }, refreshActor: mocks.refreshActor }) }
})

function detail(id, group) {
  return { plugin: { plugin_id: id, current_lifecycle: 'active', runtime_kind: 'rpc-service', runtime_abi: 'nre:rpc/v1' }, package: { version: '1.0.0', manifest: { name: id } }, instances: [{ id: `${id}-instance`, resource_group_id: group }], agent_statuses: [{ instance_id: `${id}-instance`, runtime_state: 'active' }] }
}

beforeEach(() => {
  mocks.fetchPlugins.mockReset().mockResolvedValue([{ plugin_id: 'visible' }, { plugin_id: 'foreign' }])
  mocks.fetchPluginDetail.mockReset().mockImplementation(async (id) => id === 'visible' ? detail(id, 'group-a') : detail(id, 'group-b'))
  mocks.refreshActor.mockReset()
})

describe('PluginsPage', () => {
  it('shows only plugins with instances in the actor visible resource groups', async () => {
    const wrapper = mount(PluginsPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    expect(wrapper.text()).toContain('visible')
    expect(wrapper.text()).not.toContain('foreign')
    expect(wrapper.text()).toContain('可见实例')
  })
})
