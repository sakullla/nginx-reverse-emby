import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginMarketplacePage from './PluginMarketplacePage.vue'

const mocks = vi.hoisted(() => ({
  fetchRepositorySources: vi.fn(), fetchRepositoryContents: vi.fn(), fetchPlugins: vi.fn(),
  fetchPluginPackageDetail: vi.fn(), installPlugin: vi.fn(), upgradePlugin: vi.fn()
}))
vi.mock('../../api/pluginRepositories', () => ({ fetchRepositorySources: mocks.fetchRepositorySources, fetchRepositoryContents: mocks.fetchRepositoryContents }))
vi.mock('../../api/plugins', () => ({
  fetchPlugins: mocks.fetchPlugins,
  fetchPluginPackageDetail: mocks.fetchPluginPackageDetail,
  installPlugin: mocks.installPlugin,
  upgradePlugin: mocks.upgradePlugin
}))
vi.mock('../../components/base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: {
      modelValue: { type: Boolean, required: true },
      title: { type: String, default: '' },
      subtitle: { type: String, default: '' },
      size: { type: String, default: 'md' },
      showFooter: { type: Boolean, default: false },
      closeOnClickModal: { type: Boolean, default: true }
    },
    emits: ['update:modelValue', 'confirm'],
    template: '<div v-if="modelValue" class="modal-stub"><div class="modal-title">{{ title }}</div><div class="modal-body"><slot /></div><div v-if="showFooter" class="modal-footer"><slot name="footer" /></div></div>'
  }
}))

const source = { id: 'community', kind: 'custom', risk_label: 'untrusted', purpose: 'market' }
const entry = { id: 'official.waf', name: 'WAF', version: '1.2.0', sha256: 'a'.repeat(64), runtime: { kind: 'wasm-policy' } }
const packageDetail = {
  digest: entry.sha256,
  version: entry.version,
  runtime: { kind: 'wasm-policy', abi: 'nre:policy/v1', host_scope: 'agent' },
  artifacts: [{ path: 'plugin.wasm', sha256: 'b'.repeat(64), size: 42, mode: 'readonly' }],
  resource_budget: { timeout_ms: 100, memory_bytes: 4096, concurrency: 1 },
  failure_policy: { on_error: 'fail-closed', core_fallback: 'block' },
  signature: { algorithm: 'ed25519', key_id: 'community-release' },
  permissions: ['http.inspect'], permission_diff: { added: ['http.inspect'], removed: [] },
  manifest: { id: entry.id, name: entry.name, extension_points: ['http.waf'], ui: '<script>packageCode()</script>' },
  config_schema: { type: 'object', properties: {} }
}

function mountPage() {
  return mount(PluginMarketplacePage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
  })
}

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text().includes(text))
}

beforeEach(() => {
  mocks.fetchRepositorySources.mockReset().mockResolvedValue([source])
  mocks.fetchRepositoryContents.mockReset().mockResolvedValue({ entries: [entry], directPlugin: null })
  mocks.fetchPlugins.mockReset().mockResolvedValue([])
  mocks.fetchPluginPackageDetail.mockReset().mockResolvedValue(packageDetail)
  mocks.installPlugin.mockReset().mockResolvedValue({ plugin_id: entry.id })
  mocks.upgradePlugin.mockReset().mockResolvedValue({})
})

describe('PluginMarketplacePage', () => {
  it('renders the shared page header with a back link and repository action', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('插件市场')
    expect(wrapper.find('.page-subtitle').text()).toContain('签名包投影')
    expect(wrapper.find('.back-link').attributes('href')).toBe('/plugins')
    expect(wrapper.find('.page-header__right a').attributes('href')).toBe('/plugins/repositories')
  })

  it('shows a spinner while loading', () => {
    const wrapper = mountPage()
    expect(wrapper.find('.spinner').exists()).toBe(true)
    expect(wrapper.find('.plugin-marketplace-page__loading').exists()).toBe(true)
  })

  it('shows an error empty state when the market fails to load', async () => {
    mocks.fetchRepositorySources.mockRejectedValue(new Error('backend unavailable'))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('读取失败')
    expect(wrapper.text()).toContain('backend unavailable')
  })

  it('shows an empty state when the market has no packages', async () => {
    mocks.fetchRepositoryContents.mockResolvedValue({ entries: [], directPlugin: null })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('暂无插件')
  })

  it('shows signed package facts and confirms permissions through a modal', async () => {
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('非官方来源')
    expect(wrapper.text()).toContain('wasm-policy')
    expect(wrapper.text()).toContain('nre:policy/v1')
    expect(wrapper.text()).toContain('Package SHA-256')
    expect(wrapper.text()).toContain('权限差异')
    expect(wrapper.text()).toContain('WAF fail-closed')
    expect(wrapper.text()).toContain('不能注入宿主前端代码')
    expect(wrapper.text()).not.toContain('packageCode')

    // Inline checkbox gating is replaced by a confirmation modal.
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)

    const install = buttonByText(wrapper, '安装插件')
    expect(install).toBeTruthy()
    expect(install.attributes('disabled')).toBeUndefined()

    await install.trigger('click')
    expect(wrapper.find('.modal-stub').exists()).toBe(true)
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    expect(wrapper.find('.modal-stub').text()).toContain('http.inspect')

    const confirm = buttonByText(wrapper, '确认安装')
    await confirm.trigger('click')
    await flushPromises()

    expect(mocks.installPlugin).toHaveBeenCalledWith(expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
    expect(wrapper.find('.modal-stub').exists()).toBe(false)
  })

  it('confirms an upgrade through the same modal with the upgrade payload', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_package_digest: 'c'.repeat(64) }])
    const wrapper = mountPage()
    await flushPromises()

    const upgrade = buttonByText(wrapper, '升级插件')
    expect(upgrade).toBeTruthy()
    await upgrade.trigger('click')
    expect(wrapper.find('.modal-title').text()).toBe('确认升级插件')

    const confirm = buttonByText(wrapper, '确认升级')
    await confirm.trigger('click')
    await flushPromises()

    expect(mocks.upgradePlugin).toHaveBeenCalledWith(entry.id, expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
  })

  it('disables install and shows the installed notice when the current digest is already present', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_package_digest: entry.sha256 }])
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('当前 digest 已安装')
    const install = buttonByText(wrapper, '安装插件')
    expect(install.attributes('disabled')).toBeDefined()
    await install.trigger('click')
    expect(wrapper.find('.modal-stub').exists()).toBe(false)
  })
})
