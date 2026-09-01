import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginMarketplaceDetailPage from './PluginMarketplaceDetailPage.vue'
import { messageStore } from '../../stores/messages'

const mocks = vi.hoisted(() => ({
  fetchRepositorySources: vi.fn(), fetchRepositoryContents: vi.fn(), fetchPlugins: vi.fn(),
  fetchPluginPackageDetail: vi.fn(), installPlugin: vi.fn(), upgradePlugin: vi.fn(), push: vi.fn()
}))
const routeState = vi.hoisted(() => ({
  params: { pluginId: 'official.waf' },
  query: { source: 'community' }
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mocks.push }),
  useRoute: () => routeState
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

const communitySource = { id: 'community', kind: 'custom', risk_label: 'untrusted', purpose: 'market' }
const officialSource = { id: 'official', kind: 'official', risk_label: 'official', purpose: 'market' }
const entry = {
  id: 'official.waf', name: 'WAF', version: '1.2.0', sha256: 'a'.repeat(64),
  runtime: { kind: 'wasm-policy', abi: 'nre:policy/v1', host_scope: 'agent' },
  compatibility: { host: '*', agent: '*' },
  capabilities: ['http.waf'],
  artifacts: [{ sha256: 'b'.repeat(64), size: 42 }],
  signature_key_id: 'community-release'
}
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
  return mount(PluginMarketplaceDetailPage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
  })
}

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text().includes(text))
}

function nextStep(wrapper) {
  return wrapper.find('[data-test="marketplace-next-step"]').text()
}

function actionButtons(wrapper) {
  return wrapper.findAll('button').map((button) => button.text().trim())
}

afterEach(() => {
  messageStore.clearAll()
})

beforeEach(() => {
  routeState.params = { pluginId: entry.id }
  routeState.query = { source: communitySource.id }
  mocks.fetchRepositorySources.mockReset().mockResolvedValue([communitySource])
  mocks.fetchRepositoryContents.mockReset().mockResolvedValue({ entries: [entry], directPlugin: null })
  mocks.fetchPlugins.mockReset().mockResolvedValue([])
  mocks.fetchPluginPackageDetail.mockReset().mockResolvedValue(packageDetail)
  mocks.installPlugin.mockReset().mockResolvedValue({ plugin_id: entry.id })
  mocks.upgradePlugin.mockReset().mockResolvedValue({})
  mocks.push.mockReset()
})

describe('PluginMarketplaceDetailPage', () => {
  it('shows catalog intro without downloading the signed package', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('WAF')
    expect(wrapper.find('.back-link').attributes('href')).toBe('/plugins/marketplace')
    expect(wrapper.text()).toContain('非官方来源')
    expect(wrapper.text()).toContain('未安装')
    expect(wrapper.text()).toContain('安装后把插件部署到一个节点即可在该节点上使用。')
    expect(nextStep(wrapper)).toContain('下一步：部署到一个节点')
    expect(nextStep(wrapper)).not.toContain('发布域名')
    expect(wrapper.get('[data-test="marketplace-detail-action"]').text()).toBe('安装')
    expect(wrapper.text()).toContain('市场快照只展示已签名的索引信息')
    expect(wrapper.find('.marketplace-technical').exists()).toBe(true)
    expect(wrapper.find('.marketplace-technical').element.open).toBeFalsy()
    expect(wrapper.findAll('summary').filter((node) => node.text().trim() === '技术详情')).toHaveLength(1)
    expect(wrapper.find('.package-summary__identity').exists()).toBe(false)
    expect(wrapper.text()).toContain('wasm-policy')
    expect(wrapper.text()).toContain('nre:policy/v1')
    expect(wrapper.text()).toContain('Package SHA-256')
    expect(wrapper.text()).toContain('权限差异')
    expect(wrapper.text()).toContain('WAF fail-closed')
    expect(wrapper.text()).toContain('不能注入宿主前端代码')
    expect(wrapper.text()).not.toContain('packageCode')
    expect(actionButtons(wrapper).some((text) => text.includes('部署') || text.includes('发布') || text.includes('卸载'))).toBe(false)
    expect(wrapper.text()).not.toContain('打开管理页')
    expect(mocks.fetchPluginPackageDetail).not.toHaveBeenCalled()
  })

  it('prefers the official catalog entry when source is omitted', async () => {
    routeState.query = {}
    mocks.fetchRepositorySources.mockResolvedValue([communitySource, officialSource])
    mocks.fetchRepositoryContents.mockImplementation(async (id) => {
      if (id === officialSource.id) {
        return { entries: [{ ...entry, name: '官方 WAF', sha256: 'd'.repeat(64), signature_key_id: 'official-release' }], directPlugin: null }
      }
      return { entries: [{ ...entry, name: '社区 WAF' }], directPlugin: null }
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('官方 WAF')
    expect(wrapper.text()).toContain('官方来源')
    expect(wrapper.text()).not.toContain('社区 WAF')
  })

  it('uses the requested source query when the same plugin exists in multiple catalogs', async () => {
    routeState.query = { source: communitySource.id }
    mocks.fetchRepositorySources.mockResolvedValue([officialSource, communitySource])
    mocks.fetchRepositoryContents.mockImplementation(async (id) => {
      if (id === officialSource.id) {
        return { entries: [{ ...entry, name: '官方 WAF', sha256: 'd'.repeat(64) }], directPlugin: null }
      }
      return { entries: [{ ...entry, name: '社区 WAF' }], directPlugin: null }
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('社区 WAF')
    expect(wrapper.text()).toContain('非官方来源')
  })

  it('falls back to the first matching catalog entry when there is no official source', async () => {
    routeState.query = {}
    const extra = { id: 'mirror', kind: 'custom', risk_label: 'community', purpose: 'market' }
    mocks.fetchRepositorySources.mockResolvedValue([communitySource, extra])
    mocks.fetchRepositoryContents.mockImplementation(async (id) => ({
      entries: [{ ...entry, name: id === extra.id ? '镜像 WAF' : '社区 WAF', sha256: id === extra.id ? 'e'.repeat(64) : entry.sha256 }],
      directPlugin: null
    }))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('社区 WAF')
    expect(wrapper.text()).toContain('非官方来源')
  })

  it('shows an empty state when the plugin is not in the catalog', async () => {
    routeState.params = { pluginId: 'missing.plugin' }
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('没有找到这个插件')
    expect(wrapper.find('a[href="/plugins/marketplace"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="marketplace-detail-action"]').exists()).toBe(false)
  })

  it('confirms install from the detail action and does not submit on cancel', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    await wrapper.get('[data-test="marketplace-detail-action"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    expect(wrapper.find('.modal-stub').text()).toContain('http.inspect')
    expect(wrapper.find('.modal-stub').text()).toContain('我已复核非官方来源')
    expect(wrapper.find('[data-test="marketplace-confirm-next"]').text()).toContain('下一步：部署到一个节点')
    await buttonByText(wrapper, '取消').trigger('click')
    expect(wrapper.find('.modal-stub').exists()).toBe(false)
    expect(mocks.installPlugin).not.toHaveBeenCalled()
    expect(wrapper.find('.page-title').text()).toBe('WAF')
    await wrapper.get('[data-test="marketplace-detail-action"]').trigger('click')
    await flushPromises()
    await buttonByText(wrapper, '确认安装').trigger('click')
    await flushPromises()
    expect(mocks.installPlugin).toHaveBeenCalledWith(expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('confirms an update through the same permission modal', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_version: '1.1.0', active_package_digest: 'c'.repeat(64) }])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-test="marketplace-detail-action"]').text()).toBe('更新')
    expect(nextStep(wrapper)).toContain('升级后会进入详情')
    expect(nextStep(wrapper)).toContain('下一步：部署到一个节点')
    expect(nextStep(wrapper)).not.toContain('发布域名')
    await wrapper.get('[data-test="marketplace-detail-action"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认升级插件')
    await buttonByText(wrapper, '确认升级').trigger('click')
    await flushPromises()
    expect(mocks.upgradePlugin).toHaveBeenCalledWith(entry.id, expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('opens installed detail without installing again', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_version: entry.version, active_package_digest: entry.sha256 }])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('当前版本已安装')
    expect(nextStep(wrapper)).toContain('打开详情继续部署')
    expect(wrapper.get('[data-test="marketplace-detail-action"]').text()).toBe('打开')
    await wrapper.get('[data-test="marketplace-detail-action"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').exists()).toBe(false)
    expect(mocks.installPlugin).not.toHaveBeenCalled()
    expect(mocks.upgradePlugin).not.toHaveBeenCalled()
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('previews publish as the next step when the package declares an HTTP backend', async () => {
    mocks.fetchRepositoryContents.mockResolvedValue({
      entries: [{ ...entry, description: '把站点流量交给这个插件处理。', capabilities: ['http.backend-provider'] }],
      directPlugin: null
    })
    mocks.fetchPluginPackageDetail.mockResolvedValue({
      ...packageDetail,
      manifest: {
        ...packageDetail.manifest,
        description: '把站点流量交给这个插件处理。',
        http_backend_providers: [{ id: 'default', display_name: 'Default' }]
      }
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('把站点流量交给这个插件处理。')
    expect(nextStep(wrapper)).toContain('发布')
    expect(nextStep(wrapper)).toContain('入口域名')
    await wrapper.get('[data-test="marketplace-detail-action"]').trigger('click')
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    expect(wrapper.find('[data-test="marketplace-confirm-next"]').text()).toContain('发布')
    expect(wrapper.find('.modal-stub').text()).toContain('http.inspect')
  })

  it('points an already-installed HTTP backend package at publish as the next detail step', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_package_digest: entry.sha256 }])
    mocks.fetchRepositoryContents.mockResolvedValue({
      entries: [{ ...entry, capabilities: ['http.backend-provider'] }],
      directPlugin: null
    })
    mocks.fetchPluginPackageDetail.mockResolvedValue({
      ...packageDetail,
      manifest: {
        ...packageDetail.manifest,
        http_backend_providers: [{ id: 'default', display_name: 'Default' }]
      }
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(nextStep(wrapper)).toContain('打开详情继续部署')
    expect(nextStep(wrapper)).toContain('发布域名')
    expect(wrapper.get('[data-test="marketplace-detail-action"]').text()).toBe('打开')
  })
})
