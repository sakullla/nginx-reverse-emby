import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginsPage from './PluginsPage.vue'

const mocks = vi.hoisted(() => ({ fetchPlugins: vi.fn(), fetchPluginDetail: vi.fn(), refreshActor: vi.fn() }))
vi.mock('../../api/plugins', () => ({ fetchPlugins: mocks.fetchPlugins, fetchPluginDetail: mocks.fetchPluginDetail }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return { ...actual, useAccessControl: () => ({ actor: { value: { permissions: ['resource.read'], visible_resource_groups: ['group-a'] } }, refreshActor: mocks.refreshActor }) }
})

const mountedWrappers = []

function detail(id, group, overrides = {}) {
  return {
    plugin: {
      plugin_id: id,
      current_lifecycle: 'active',
      runtime_kind: 'rpc-service',
      runtime_abi: 'nre:rpc/v1',
      active_source_kind: 'official',
      active_source_risk_label: '低',
      ...(overrides.plugin || {})
    },
    package: { version: '1.0.0', manifest: { name: id }, ...(overrides.package || {}) },
    instances: [{ id: `${id}-instance`, resource_group_id: group }],
    agent_statuses: [{ instance_id: `${id}-instance`, runtime_state: 'active' }]
  }
}

function mountPage() {
  const wrapper = mount(PluginsPage, {
    global: {
      stubs: {
        RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
        AgentSearchSelect: { props: ['modelValue', 'agents'], template: '<div />' }
      }
    }
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

afterEach(() => {
  while (mountedWrappers.length) {
    mountedWrappers.pop().unmount()
  }
  document.body.innerHTML = ''
})

beforeEach(() => {
  mocks.fetchPlugins.mockReset().mockResolvedValue([{ plugin_id: 'visible' }, { plugin_id: 'foreign' }])
  mocks.fetchPluginDetail.mockReset().mockImplementation(async (id) => id === 'visible' ? detail(id, 'group-a') : detail(id, 'group-b'))
  mocks.refreshActor.mockReset()
})

describe('PluginsPage', () => {
  it('shows only plugins with instances in the actor visible resource groups', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('visible')
    expect(wrapper.text()).not.toContain('foreign')
    expect(wrapper.text()).toContain('可见实例')
  })

  it('renders the shared page header title and subtitle', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('已安装插件')
    expect(wrapper.find('.page-subtitle').text()).toContain('资源组')
    expect(wrapper.find('.page-header__right a').attributes('href')).toBe('/plugins/marketplace')
  })

  it('shows a spinner while loading', () => {
    const wrapper = mountPage()
    expect(wrapper.find('.spinner').exists()).toBe(true)
    expect(wrapper.find('.plugins-page__loading').exists()).toBe(true)
  })

  it('shows an error empty state when loading fails', async () => {
    mocks.fetchPlugins.mockRejectedValue(new Error('backend unavailable'))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('读取失败')
    expect(wrapper.text()).toContain('backend unavailable')
  })

  it('shows an empty state when there are no visible plugins', async () => {
    mocks.fetchPlugins.mockResolvedValue([])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('暂无插件')
  })

  it('filters plugins by search query', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'alpha' }, { plugin_id: 'beta' }])
    mocks.fetchPluginDetail.mockImplementation(async (id) => id === 'alpha' ? detail(id, 'group-a') : detail(id, 'group-a'))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).toContain('beta')

    await wrapper.find('input[aria-label="搜索资源"]').setValue('alpha')
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).not.toContain('beta')
  })

  it('filters plugins by lifecycle', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'alpha' }, { plugin_id: 'beta' }])
    mocks.fetchPluginDetail.mockImplementation(async (id) =>
      id === 'alpha'
        ? detail(id, 'group-a')
        : detail(id, 'group-a', { plugin: { current_lifecycle: 'disabled' } })
    )
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).toContain('beta')

    wrapper.findComponent({ name: 'ResourceListFilterBar' }).vm.$emit('update:filter', { key: 'lifecycle', value: 'active' })
    await flushPromises()
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).not.toContain('beta')
  })

  it('exposes a lifecycle chip filter to the shared filter bar', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const filterBar = wrapper.findComponent({ name: 'ResourceListFilterBar' })
    const fields = filterBar.props('filterFields')
    const lifecycle = fields.find((field) => field.key === 'lifecycle')
    expect(lifecycle.type).toBe('chip')
    expect(lifecycle.options.map((option) => option.value)).toContain('active')
    expect(lifecycle.options.map((option) => option.value)).toContain('disabled')
  })
})
