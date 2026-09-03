import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginMarketplacePage from './PluginMarketplacePage.vue'
import { messageStore } from '../../stores/messages'

const mocks = vi.hoisted(() => ({
  fetchRepositorySources: vi.fn(), fetchRepositoryContents: vi.fn(), fetchPlugins: vi.fn(),
  fetchPluginPackageDetail: vi.fn(), installPlugin: vi.fn(), upgradePlugin: vi.fn(), push: vi.fn(),
  refreshRepositorySource: vi.fn(), createRepositorySource: vi.fn(), updateRepositorySource: vi.fn(),
  deleteRepositorySource: vi.fn()
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))
vi.mock('../../api/pluginRepositories', () => ({
  fetchRepositorySources: mocks.fetchRepositorySources,
  fetchRepositoryContents: mocks.fetchRepositoryContents,
  refreshRepositorySource: mocks.refreshRepositorySource,
  createRepositorySource: mocks.createRepositorySource,
  updateRepositorySource: mocks.updateRepositorySource,
  deleteRepositorySource: mocks.deleteRepositorySource
}))
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
      closeOnClickModal: { type: Boolean, default: true },
      dataTest: { type: String, default: '' }
    },
    emits: ['update:modelValue', 'confirm'],
    template: '<div v-if="modelValue" class="modal-stub" :data-test="dataTest"><div class="modal-title">{{ title }}</div><div class="modal-body"><slot /></div><div v-if="showFooter" class="modal-footer"><slot name="footer" /></div></div>'
  }
}))

const source = { id: 'community', name: 'Community', kind: 'custom', risk_label: 'untrusted', purpose: 'market' }
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
  return mount(PluginMarketplacePage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
  })
}

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text().includes(text))
}

function detailHref() {
  return `/plugins/marketplace/${encodeURIComponent(entry.id)}?source=${encodeURIComponent(source.id)}`
}

afterEach(() => {
  messageStore.clearAll()
})

beforeEach(() => {
  localStorage.removeItem('view:plugin-marketplace')
  mocks.fetchRepositorySources.mockReset().mockResolvedValue([source])
  mocks.fetchRepositoryContents.mockReset().mockResolvedValue({ entries: [entry], directPlugin: null })
  mocks.fetchPlugins.mockReset().mockResolvedValue([])
  mocks.fetchPluginPackageDetail.mockReset().mockResolvedValue(packageDetail)
  mocks.installPlugin.mockReset().mockResolvedValue({ plugin_id: entry.id })
  mocks.upgradePlugin.mockReset().mockResolvedValue({})
  mocks.refreshRepositorySource.mockReset().mockResolvedValue({ source_id: source.id })
  mocks.createRepositorySource.mockReset().mockResolvedValue(source)
  mocks.updateRepositorySource.mockReset().mockResolvedValue(source)
  mocks.deleteRepositorySource.mockReset().mockResolvedValue({ ok: true })
  mocks.push.mockReset()
})

describe('PluginMarketplacePage', () => {
  it('renders the shared page header with a back link and repository action', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('插件市场')
    expect(wrapper.find('.page-subtitle').text()).toContain('部署')
    expect(wrapper.find('.page-subtitle').text()).toContain('下一步')
    expect(wrapper.find('.page-subtitle').text()).toContain('发布')
    expect(wrapper.find('.back-link').attributes('href')).toBe('/plugins')
    expect(wrapper.find('a[href="/plugins/repositories"]').exists()).toBe(false)
    const repoButton = wrapper.get('[data-test="marketplace-repositories"]')
    expect(repoButton.element.tagName).toBe('BUTTON')
    expect(repoButton.text()).toBe('插件仓库')
    expect(repoButton.classes()).toContain('btn-secondary')
    expect(wrapper.get('[data-test="marketplace-catalog-refresh"]').text()).toBe('更新')
    expect(wrapper.get('[data-test="marketplace-catalog-updated-at"]').text()).toBe('尚未更新')
  })

  it('opens an in-page repository modal instead of navigating to the repositories page', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const contentsCalls = mocks.fetchRepositoryContents.mock.calls.length
    await wrapper.get('[data-test="marketplace-repositories"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('插件市场')
    expect(wrapper.get('[data-test="plugin-repositories-modal"]').exists()).toBe(true)
    expect(wrapper.find('.modal-title').text()).toBe('插件仓库')
    expect(wrapper.text()).toContain('Community')
    expect(mocks.push).not.toHaveBeenCalled()
    expect(wrapper.find('a[href="/plugins/repositories"]').exists()).toBe(false)
    await wrapper.find('.repository-card').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="repository-source-inspect"]').exists()).toBe(true)
    expect(wrapper.find('.repository-packages').exists()).toBe(false)
    expect(wrapper.find('.repository-package-list').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('市场条目')
    expect(mocks.fetchRepositoryContents).toHaveBeenCalledTimes(contentsCalls)
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('refreshes each catalog source then reloads the catalog', async () => {
    mocks.fetchRepositorySources.mockResolvedValue([{ ...source, last_completed_at: '2026-08-10T08:00:00Z' }])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[data-test="marketplace-catalog-updated-at"]').text()).toBe('2026/08/10 08:00:00')
    const contentsCalls = mocks.fetchRepositoryContents.mock.calls.length
    await wrapper.get('[data-test="marketplace-catalog-refresh"]').trigger('click')
    await flushPromises()
    expect(mocks.refreshRepositorySource).toHaveBeenCalledWith(source.id)
    expect(mocks.fetchRepositoryContents.mock.calls.length).toBeGreaterThan(contentsCalls)
    expect(wrapper.get('[data-test="marketplace-catalog-updated-at"]').text()).toBe('2026/08/10 08:00:00')
    expect(wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).exists()).toBe(true)
  })

  it('keeps successful catalog sources and the last completed time when one refresh fails', async () => {
    const official = {
      id: 'official',
      name: 'Official',
      kind: 'official',
      purpose: 'market',
      last_completed_at: '2026-08-10T08:00:00Z'
    }
    const community = { ...source, last_completed_at: '2026-08-09T08:00:00Z' }
    mocks.fetchRepositorySources.mockResolvedValue([official, community])
    mocks.fetchRepositoryContents.mockImplementation(async (id) => (
      id === official.id
        ? { entries: [{ ...entry, id: 'official.helper', name: 'Helper' }], directPlugin: null }
        : { entries: [entry], directPlugin: null }
    ))
    mocks.refreshRepositorySource
      .mockResolvedValueOnce({ source_id: official.id })
      .mockRejectedValueOnce(new Error('community refresh failed'))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('Helper')
    expect(wrapper.text()).toContain('WAF')
    expect(wrapper.get('[data-test="marketplace-catalog-updated-at"]').text()).toBe('2026/08/10 08:00:00')
    await wrapper.get('[data-test="marketplace-catalog-refresh"]').trigger('click')
    await flushPromises()
    expect(mocks.refreshRepositorySource).toHaveBeenCalledWith(official.id)
    expect(mocks.refreshRepositorySource).toHaveBeenCalledWith(community.id)
    expect(wrapper.text()).toContain('Helper')
    expect(wrapper.text()).toContain('WAF')
    expect(wrapper.get('[data-test="marketplace-catalog-updated-at"]').text()).toBe('2026/08/10 08:00:00')
    expect(wrapper.get('[data-test="marketplace-catalog-updated-at"]').text()).not.toBe('尚未更新')
    expect(messageStore.state.messages.map((item) => item.text).join('\n')).toContain('community refresh failed')
  })

  it('shows a spinner while loading', () => {
    const wrapper = mountPage()
    expect(wrapper.find('.spinner').exists()).toBe(true)
    expect(wrapper.find('.plugin-marketplace-page__loading').exists()).toBe(true)
  })

  it('shows preview catalog names when the market fails to load', async () => {
    mocks.fetchRepositorySources.mockRejectedValue(new Error('backend unavailable'))
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('Emby 助手')
    expect(wrapper.text()).toContain('网站防火墙')
    expect(wrapper.text()).not.toContain('official.emby-helper')
    expect(wrapper.text()).toContain('下一步')
  })

  it('renders the catalog name from the signed package instead of unnamed fallback', async () => {
    mocks.fetchRepositoryContents.mockResolvedValue({
      entries: [{ id: 'cloudflare-dns', name: 'Cloudflare DNS', version: '0.1.5', sha256: 'd'.repeat(64), description: '按域名后缀解析 Cloudflare DNS Token' }],
      directPlugin: null
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('.marketplace-card__name').text()).toBe('Cloudflare DNS')
    expect(wrapper.text()).not.toContain('未命名插件')
    expect(wrapper.text()).not.toContain('cloudflare-dns')
  })

  it('keeps the plugin id when the catalog has not projected a display name', async () => {
    mocks.fetchRepositoryContents.mockResolvedValue({
      entries: [{ id: 'accelerator-sources', version: '0.1.0', sha256: 'e'.repeat(64), description: '为零配置发布的自有域名提供 Docker、GitHub 与 Hugging Face 加速' }],
      directPlugin: null
    })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('.marketplace-card__name').text()).toBe('accelerator-sources')
    expect(wrapper.text()).not.toContain('未命名插件')
  })

  it('links the card body and name to the marketplace detail route instead of opening an inspect modal', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const link = wrapper.get(`[data-test="marketplace-detail-link-${entry.id}"]`)
    expect(link.attributes('href')).toBe(detailHref())
    expect(link.text()).toContain('WAF')
    expect(wrapper.find('.modal-stub').exists()).toBe(false)
    expect(wrapper.find('.modal-title').exists()).toBe(false)
    expect(mocks.fetchPluginPackageDetail).not.toHaveBeenCalled()
  })

  it('shows visible install text on the card and opens confirm immediately while package detail is loading', async () => {
    let resolveDetail
    mocks.fetchPluginPackageDetail.mockReturnValue(new Promise((resolve) => { resolveDetail = resolve }))
    const wrapper = mountPage()
    await flushPromises()
    const action = wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`)
    expect(action.text()).toBe('安装')
    expect(action.element.tagName).toBe('BUTTON')
    await action.trigger('click')
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    const loading = wrapper.get('[data-test="marketplace-detail-loading"]')
    expect(loading.text()).toContain('正在连接市场源')
    expect(loading.text()).toContain('下载签名包')
    expect(loading.text()).toContain('已等待 0 秒')
    expect(loading.find('[role="progressbar"]').exists()).toBe(true)
    expect(buttonByText(wrapper, '下载中…').attributes('disabled')).toBeDefined()
    resolveDetail(packageDetail)
    await flushPromises()
    expect(wrapper.find('[data-test="marketplace-detail-loading"]').exists()).toBe(false)
    expect(buttonByText(wrapper, '确认安装').attributes('disabled')).toBeUndefined()
  })

  it('shows download size and elapsed time on the confirm progress bar', async () => {
    vi.useFakeTimers()
    mocks.fetchRepositoryContents.mockResolvedValue({
      entries: [{ ...entry, blob_size: 12 * 1024 * 1024 }],
      directPlugin: null
    })
    let resolveDetail
    mocks.fetchPluginPackageDetail.mockReturnValue(new Promise((resolve) => { resolveDetail = resolve }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).trigger('click')
    await vi.advanceTimersByTimeAsync(2000)
    const loading = wrapper.get('[data-test="marketplace-detail-loading"]')
    expect(loading.text()).toContain('正在下载签名包（约 12 MB）')
    expect(loading.text()).toContain('已等待 2 秒')
    expect(loading.text()).toContain('校验完整性')
    resolveDetail(packageDetail)
    await flushPromises()
    vi.useRealTimers()
    wrapper.unmount()
  })

  it('explains a package-detail timeout on the confirm dialog', async () => {
    mocks.fetchPluginPackageDetail.mockRejectedValue(Object.assign(new Error('timeout of 180000ms exceeded'), { code: 'ECONNABORTED' }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="marketplace-action-error"]').exists()).toBe(false)
    expect(messageStore.state.messages.map((item) => item.text).join('\n')).toContain('读取插件包超时')
    expect(buttonByText(wrapper, '重试安装')).toBeTruthy()
    expect(buttonByText(wrapper, '重试安装').attributes('disabled')).toBeUndefined()
  })

  it('installs from the card action after permission confirm and does not submit on cancel', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const action = wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`)
    expect(action.text()).toBe('安装')
    await action.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    await buttonByText(wrapper, '取消').trigger('click')
    expect(wrapper.find('.modal-stub').exists()).toBe(false)
    expect(mocks.installPlugin).not.toHaveBeenCalled()
    await action.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    expect(mocks.fetchPluginPackageDetail).toHaveBeenCalledTimes(1)
    await buttonByText(wrapper, '确认安装').trigger('click')
    await flushPromises()
    expect(mocks.installPlugin).toHaveBeenCalledWith(expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('labels an installed package as open on the card and navigates to installed detail', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_version: entry.version, active_package_digest: entry.sha256 }])
    const wrapper = mountPage()
    await flushPromises()
    const action = wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`)
    expect(action.text()).toBe('打开')
    await action.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-stub').exists()).toBe(false)
    expect(mocks.installPlugin).not.toHaveBeenCalled()
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('shows a visible update action for an upgradable package', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_version: '1.1.0', active_package_digest: 'c'.repeat(64) }])
    const wrapper = mountPage()
    await flushPromises()
    const action = wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`)
    expect(action.text()).toBe('更新')
    expect(wrapper.get('[data-test="marketplace-catalog-refresh"]').text()).toBe('更新')
    await action.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认升级插件')
    expect(mocks.refreshRepositorySource).not.toHaveBeenCalled()
    await buttonByText(wrapper, '确认升级').trigger('click')
    await flushPromises()
    expect(mocks.upgradePlugin).toHaveBeenCalledWith(entry.id, expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('shows an empty state when the market has no packages', async () => {
    mocks.fetchRepositoryContents.mockResolvedValue({ entries: [], directPlugin: null })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('暂无插件')
    expect(wrapper.text()).toContain('下一步')
    expect(wrapper.find('a[href="/plugins/repositories"]').exists()).toBe(false)
    const repositoryButtons = wrapper.findAll('button').filter((button) => button.text() === '插件仓库')
    expect(repositoryButtons.length).toBeGreaterThanOrEqual(2)
    await wrapper.get('[data-test="marketplace-repositories-empty"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="plugin-repositories-modal"]').exists()).toBe(true)
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('does not advertise an upgrade when the version is unchanged but the catalog digest was rebuilt', async () => {
    mocks.fetchPlugins.mockResolvedValue([{
      plugin_id: entry.id,
      active_version: entry.version,
      active_package_digest: 'c'.repeat(64)
    }])
    const wrapper = mountPage()
    await flushPromises()

    expect(wrapper.text()).toContain('已安装')
    expect(wrapper.text()).not.toContain('可升级')
    expect(wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).text()).toBe('打开')
  })

  it('stays on the market and does not navigate when install fails', async () => {
    mocks.installPlugin.mockRejectedValue(new Error('source rejected'))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).trigger('click')
    await flushPromises()
    await buttonByText(wrapper, '确认安装').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="marketplace-action-error"]').exists()).toBe(false)
    expect(messageStore.state.messages.map((item) => item.text).join('\n')).toContain('source rejected')
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    expect(buttonByText(wrapper, '重试安装')).toBeTruthy()
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('retries upgrade after a pending-operation conflict instead of ignoring the confirm click', async () => {
    mocks.fetchPlugins.mockResolvedValue([{
      plugin_id: entry.id,
      active_package_digest: 'c'.repeat(64),
      pending_operation_id: 'op-configure',
      pending_kind: 'configure'
    }])
    mocks.upgradePlugin
      .mockRejectedValueOnce(new Error('plugin state conflict: another plugin operation is already pending'))
      .mockResolvedValueOnce({})
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).trigger('click')
    await flushPromises()
    await buttonByText(wrapper, '确认升级').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="marketplace-action-error"]').exists()).toBe(false)
    expect(messageStore.state.messages.map((item) => item.text).join('\n')).toContain('未完成的操作')
    expect(wrapper.get('[data-test="marketplace-pending-detail"]').attributes('href')).toBe(`/plugins/${encodeURIComponent(entry.id)}`)
    expect(wrapper.find('[data-test="marketplace-confirm-next"]').exists()).toBe(false)
    expect(mocks.upgradePlugin).toHaveBeenCalledTimes(1)
    expect(mocks.push).not.toHaveBeenCalled()
    await wrapper.get('[data-test="marketplace-confirm-submit"]').trigger('click')
    await flushPromises()
    expect(mocks.upgradePlugin).toHaveBeenCalledTimes(2)
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('does not tell the user to open the second plugin when another upgrade is still applying', async () => {
    const helper = {
      ...entry,
      id: 'official.emby-helper',
      name: 'Emby 助手',
      version: '1.4.2',
      sha256: 'd'.repeat(64)
    }
    mocks.fetchRepositoryContents.mockResolvedValue({ entries: [entry, helper], directPlugin: null })
    mocks.fetchPlugins.mockResolvedValue([
      { plugin_id: entry.id, active_package_digest: 'c'.repeat(64), pending_operation_id: 'op-upgrade', pending_kind: 'upgrade', pending_target_digest: entry.sha256 },
      { plugin_id: helper.id, active_package_digest: 'e'.repeat(64) }
    ])
    mocks.fetchPluginPackageDetail.mockImplementation(async (selection) => ({
      ...packageDetail,
      digest: selection.digest,
      version: selection.version,
      manifest: { ...packageDetail.manifest, id: selection.plugin_id, name: selection.plugin_id === helper.id ? helper.name : entry.name }
    }))
    mocks.upgradePlugin.mockRejectedValue(new Error('plugin state conflict: another plugin operation is already pending'))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${helper.id}"]`).trigger('click')
    await flushPromises()
    await buttonByText(wrapper, '确认升级').trigger('click')
    await flushPromises()
    const text = messageStore.state.messages.map((item) => item.text).join('\n')
    expect(text).toContain('另一个插件的升级还在节点上应用')
    expect(text).toContain('这个插件本身正常')
    expect(text).not.toContain('打开详情查看进度')
    expect(wrapper.find('[data-test="marketplace-pending-detail"]').exists()).toBe(false)
    expect(mocks.upgradePlugin).toHaveBeenCalledWith(helper.id, expect.objectContaining({
      plugin_id: helper.id, digest: helper.sha256
    }))
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('does not treat a marketplace refresh conflict as this plugin being broken', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_package_digest: 'c'.repeat(64) }])
    mocks.upgradePlugin.mockRejectedValue(new Error('plugin state conflict: official marketplace branch changed concurrently'))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).trigger('click')
    await flushPromises()
    await buttonByText(wrapper, '确认升级').trigger('click')
    await flushPromises()
    const text = messageStore.state.messages.map((item) => item.text).join('\n')
    expect(text).toContain('市场目录刚刷新或正在刷新')
    expect(text).toContain('这个插件本身正常')
    expect(wrapper.find('[data-test="marketplace-pending-detail"]').exists()).toBe(false)
  })

  it('still submits upgrade when the same catalog digest is already pending', async () => {
    mocks.fetchPlugins
      .mockResolvedValueOnce([{ plugin_id: entry.id, active_package_digest: 'c'.repeat(64) }])
      .mockResolvedValue([{
        plugin_id: entry.id,
        active_package_digest: 'c'.repeat(64),
        pending_operation_id: 'op-upgrade',
        pending_kind: 'upgrade',
        pending_target_digest: entry.sha256
      }])
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).trigger('click')
    await flushPromises()
    await buttonByText(wrapper, '确认升级').trigger('click')
    await flushPromises()
    expect(mocks.upgradePlugin).toHaveBeenCalledWith(entry.id, expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256
    }))
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('does not skip upgrade when a non-upgrade pending operation happens to share the catalog digest', async () => {
    mocks.fetchPlugins.mockResolvedValue([{
      plugin_id: entry.id,
      active_package_digest: 'c'.repeat(64),
      pending_operation_id: 'op-configure',
      pending_kind: 'configure',
      pending_target_digest: entry.sha256
    }])
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).trigger('click')
    await flushPromises()
    await buttonByText(wrapper, '确认升级').trigger('click')
    await flushPromises()
    expect(mocks.upgradePlugin).toHaveBeenCalledTimes(1)
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('switches the catalog to a list table with a visible install action and a name link', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.plugin-marketplace-catalog').exists()).toBe(true)
    await wrapper.get('button[title="列表视图"]').trigger('click')
    expect(wrapper.find('.plugin-marketplace-catalog').exists()).toBe(false)
    const table = wrapper.get('[data-test="marketplace-table"]')
    expect(table.text()).toContain('WAF')
    expect(table.text()).toContain('安装')
    expect(table.get(`[data-test="marketplace-detail-link-${entry.id}"]`).attributes('href')).toBe(detailHref())
    await table.get('tbody tr').trigger('click')
    expect(mocks.push).toHaveBeenCalledWith(detailHref())
    const action = table.get(`[data-test="marketplace-card-action-${entry.id}"]`)
    expect(action.text()).toBe('安装')
    expect(action.classes()).toContain('btn-sm')
    expect(action.classes()).toContain('btn-primary')
    await action.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
  })
})
