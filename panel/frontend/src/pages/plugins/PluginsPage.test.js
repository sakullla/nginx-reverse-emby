import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginsPage from './PluginsPage.vue'

const mocks = vi.hoisted(() => ({
  fetchPlugins: vi.fn(),
  fetchPluginDetail: vi.fn(),
  refreshActor: vi.fn(),
  actor: { permissions: ['resource.read'], visible_resource_groups: ['group-a'] }
}))
vi.mock('../../api/plugins', () => ({ fetchPlugins: mocks.fetchPlugins, fetchPluginDetail: mocks.fetchPluginDetail }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      actor: { get value() { return mocks.actor } },
      refreshActor: mocks.refreshActor
    })
  }
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
    package: {
      version: '1.0.0',
      ...(overrides.package || {}),
      manifest: { name: id, ...((overrides.package && overrides.package.manifest) || {}) }
    },
    instances: overrides.instances !== undefined ? overrides.instances : [{ id: `${id}-instance`, resource_group_id: group, targets: ['edge-a'] }],
    agent_statuses: overrides.agent_statuses !== undefined ? overrides.agent_statuses : [{ instance_id: `${id}-instance`, runtime_state: 'active' }],
    published_entries: overrides.published_entries || []
  }
}

function withHTTPBackend(value, providers = [{ id: 'default', display_name: 'Default' }]) {
  return detail(value.plugin.plugin_id, value.instances[0]?.resource_group_id || 'group-a', {
    ...value,
    package: {
      ...(value.package || {}),
      manifest: { ...(value.package?.manifest || {}), http_backend_providers: providers }
    }
  })
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
  mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['group-a'] }
  mocks.fetchPlugins.mockReset().mockResolvedValue([{ plugin_id: 'visible' }, { plugin_id: 'foreign' }])
  mocks.fetchPluginDetail.mockReset().mockImplementation(async (id) => id === 'visible' ? detail(id, 'group-a') : detail(id, 'group-b'))
  mocks.refreshActor.mockReset()
})

describe('PluginsPage', () => {
  it('keeps installed plugins without visible instances and hides foreign instance facts', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('visible')
    expect(wrapper.text()).toContain('foreign')
    expect(wrapper.text()).toContain('已可用')
    expect(wrapper.text()).toContain('尚未部署')
    expect(wrapper.text()).toContain('官方来源')
    expect(wrapper.text()).not.toContain('nre:rpc/v1')
    expect(wrapper.text()).not.toContain('可见实例')
  })

  it('shows an installed plugin with no instances so the actor can open detail', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'fresh' }])
    mocks.fetchPluginDetail.mockResolvedValue({
      plugin: { plugin_id: 'fresh', current_lifecycle: 'disabled', active_source_kind: 'official' },
      package: { version: '1.0.0', manifest: { name: 'Fresh Plugin' } },
      instances: [],
      agent_statuses: [],
      published_entries: []
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('Fresh Plugin')
    expect(wrapper.text()).toContain('尚未部署')
    expect(wrapper.find('a.plugin-card-link').attributes('href')).toBe('/plugins/fresh')
  })

  it('renders the shared page header title and subtitle', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('已安装插件')
    expect(wrapper.find('.page-subtitle').text()).toContain('尚未部署')
    expect(wrapper.find('.page-subtitle').text()).toMatch(/部署|发布/)
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
    expect(wrapper.text()).toContain('Cloudflare DNS')
    expect(wrapper.text()).toContain('打开管理页')
  })

  it('shows an empty state that points to the marketplace', async () => {
    mocks.fetchPlugins.mockResolvedValue([])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('还没有已安装的插件')
    expect(wrapper.text()).toContain('下一步：到插件市场安装一个插件')
    expect(wrapper.find('a[href="/plugins/marketplace"]').exists()).toBe(true)
  })

  it('distinguishes undeployed, unpublished HTTP, available, and abnormal plugins', async () => {
    mocks.fetchPlugins.mockResolvedValue([
      { plugin_id: 'fresh' },
      { plugin_id: 'waiting' },
      { plugin_id: 'ready' },
      { plugin_id: 'broken' }
    ])
    mocks.fetchPluginDetail.mockImplementation(async (id) => {
      if (id === 'fresh') {
        return detail(id, 'group-a', { instances: [], agent_statuses: [], published_entries: [] })
      }
      if (id === 'waiting') {
        return withHTTPBackend(detail(id, 'group-a', { published_entries: [] }))
      }
      if (id === 'ready') {
        return withHTTPBackend(detail(id, 'group-a', {
          published_entries: [{
            rule_id: 12,
            agent_id: 'edge-a',
            frontend_url: 'https://ready.example.com',
            enabled: true,
            accessible: true
          }]
        }))
      }
      return withHTTPBackend(detail(id, 'group-a', {
        published_entries: [{
          rule_id: 13,
          agent_id: 'edge-a',
          frontend_url: 'https://broken.example.com',
          enabled: true,
          accessible: false
        }]
      }))
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('[data-test="plugin-task-status-undeployed"]').text()).toBe('尚未部署')
    expect(wrapper.find('[data-test="plugin-task-status-unpublished"]').text()).toBe('待发布')
    expect(wrapper.find('[data-test="plugin-task-status-available"]').text()).toBe('已可用')
    expect(wrapper.find('[data-test="plugin-task-status-abnormal"]').text()).toBe('异常')
    expect(wrapper.text()).toContain('https://ready.example.com')
  })

  it('does not treat a deployed non-HTTP plugin as waiting to publish', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'helper' }])
    mocks.fetchPluginDetail.mockResolvedValue(detail('helper', 'group-a', { published_entries: [] }))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('[data-test="plugin-task-status-available"]').text()).toBe('已可用')
    expect(wrapper.find('[data-test="plugin-task-status-unpublished"]').exists()).toBe(false)
  })

  it('does not show hidden-group published entries when no instances remain visible', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'foreign' }])
    mocks.fetchPluginDetail.mockResolvedValue(withHTTPBackend(detail('foreign', 'group-b', {
      published_entries: [{
        rule_id: 22,
        agent_id: 'edge-b',
        frontend_url: 'https://hidden-group.example.com',
        enabled: true,
        accessible: true
      }]
    })))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('[data-test="plugin-task-status-undeployed"]').text()).toBe('尚未部署')
    expect(wrapper.text()).not.toContain('https://hidden-group.example.com')
    expect(wrapper.find('[data-test="plugin-task-status-available"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="plugin-task-status-unpublished"]').exists()).toBe(false)
  })

  it('does not let another group entry mark a visible unpublished instance as available', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'split' }])
    mocks.fetchPluginDetail.mockResolvedValue(withHTTPBackend(detail('split', 'group-a', {
      instances: [
        { id: 'split-visible', resource_group_id: 'group-a', targets: ['edge-a'], bindings: [] },
        { id: 'split-hidden', resource_group_id: 'group-b', targets: ['edge-b'], bindings: [{ target_agent_id: 'edge-b' }] }
      ],
      agent_statuses: [
        { instance_id: 'split-visible', runtime_state: 'active' },
        { instance_id: 'split-hidden', runtime_state: 'active' }
      ],
      published_entries: [{
        rule_id: 21,
        agent_id: 'edge-b',
        frontend_url: 'https://other-group.example.com',
        enabled: true,
        accessible: true
      }]
    })))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('[data-test="plugin-task-status-unpublished"]').text()).toBe('待发布')
    expect(wrapper.text()).not.toContain('https://other-group.example.com')
    expect(wrapper.find('[data-test="plugin-task-status-available"]').exists()).toBe(false)
  })

  it('keeps every published entry for an admin actor', async () => {
    mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'ready' }])
    mocks.fetchPluginDetail.mockResolvedValue(withHTTPBackend(detail('ready', 'group-b', {
      instances: [{ id: 'ready-instance', resource_group_id: 'group-b', targets: ['edge-b'] }],
      published_entries: [{
        rule_id: 12,
        agent_id: 'edge-b',
        frontend_url: 'https://admin-visible.example.com',
        enabled: true,
        accessible: true
      }]
    })))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('[data-test="plugin-task-status-available"]').text()).toBe('已可用')
    expect(wrapper.text()).toContain('https://admin-visible.example.com')
  })

  it('marks failed node runtimes as abnormal even without a published entry', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'helper' }])
    mocks.fetchPluginDetail.mockResolvedValue(detail('helper', 'group-a', {
      agent_statuses: [{ instance_id: 'helper-instance', runtime_state: 'failed' }]
    }))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('[data-test="plugin-task-status-abnormal"]').text()).toBe('异常')
    expect(wrapper.find('[data-test="plugin-task-status-unpublished"]').exists()).toBe(false)
  })

  it('filters plugins by search query', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'alpha' }, { plugin_id: 'beta' }])
    mocks.fetchPluginDetail.mockImplementation(async (id) => id === 'alpha' ? detail(id, 'group-a') : detail(id, 'group-a'))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).toContain('beta')

    await wrapper.find('input[aria-label="搜索插件名称"]').setValue('alpha')
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).not.toContain('beta')
  })

  it('filters plugins by task status', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: 'alpha' }, { plugin_id: 'beta' }])
    mocks.fetchPluginDetail.mockImplementation(async (id) => (
      id === 'alpha'
        ? withHTTPBackend(detail(id, 'group-a', {
          published_entries: [{
            rule_id: 8,
            agent_id: 'edge-a',
            frontend_url: 'https://alpha.example.com',
            enabled: true,
            accessible: true
          }]
        }))
        : detail(id, 'group-a', { instances: [], agent_statuses: [] })
    ))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).toContain('beta')

    const chips = wrapper.findAll('.plugins-chip')
    await chips.find((chip) => chip.text() === '已可用').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).not.toContain('beta')
  })

  it('exposes task-status chips on the installed plugin list', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.findAll('.plugins-chip').map((chip) => chip.text())).toEqual(['全部', '尚未部署', '待发布', '已可用', '异常'])
  })
})
