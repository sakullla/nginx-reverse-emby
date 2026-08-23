import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginMarketplacePage from './PluginMarketplacePage.vue'
import { messageStore } from '../../stores/messages'

const mocks = vi.hoisted(() => ({
  fetchRepositorySources: vi.fn(), fetchRepositoryContents: vi.fn(), fetchPlugins: vi.fn(),
  fetchPluginPackageDetail: vi.fn(), installPlugin: vi.fn(), upgradePlugin: vi.fn(), push: vi.fn()
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))
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
  mocks.push.mockReset()
})

async function openFirstPackage(wrapper) {
  await wrapper.get('.marketplace-card').trigger('click')
  await flushPromises()
}

function nextStep(wrapper) {
  return wrapper.find('[data-test="marketplace-next-step"]').text()
}

describe('PluginMarketplacePage', () => {
  it('renders the shared page header with a back link and repository action', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.page-title').text()).toBe('插件市场')
    expect(wrapper.find('.page-subtitle').text()).toContain('部署')
    expect(wrapper.find('.page-subtitle').text()).toContain('下一步')
    expect(wrapper.find('.page-subtitle').text()).toContain('发布')
    expect(wrapper.find('.back-link').attributes('href')).toBe('/plugins')
    expect(wrapper.find('.page-header__right a').attributes('href')).toBe('/plugins/repositories')
    expect(wrapper.find('.page-header__right a').text()).toBe('高级：仓库源')
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

  it('opens catalog details without resolving or downloading the package', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await openFirstPackage(wrapper)
    expect(wrapper.find('.modal-title').text()).toBe('WAF')
    expect(wrapper.text()).toContain('市场快照只展示已签名的索引信息')
    expect(mocks.fetchPluginPackageDetail).not.toHaveBeenCalled()
  })

  it('opens confirm immediately and keeps install disabled while package detail is loading', async () => {
    let resolveDetail
    mocks.fetchPluginPackageDetail.mockReturnValue(new Promise((resolve) => { resolveDetail = resolve }))
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).trigger('click')
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

  it('installs from the card action without requiring a second inspect click', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const action = wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`)
    expect(action.attributes('title')).toBe('安装')
    expect(action.attributes('aria-label')).toBe('安装')
    await action.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    await buttonByText(wrapper, '取消').trigger('click')
    expect(wrapper.find('.modal-stub').exists()).toBe(false)
    expect(wrapper.find('.modal-title').exists()).toBe(false)
    await action.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    expect(mocks.fetchPluginPackageDetail).toHaveBeenCalledTimes(1)
    await buttonByText(wrapper, '确认安装').trigger('click')
    await flushPromises()
    expect(mocks.installPlugin).toHaveBeenCalled()
  })

  it('labels an installed package as open-detail on the card', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_version: entry.version, active_package_digest: entry.sha256 }])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).attributes('title')).toBe('打开详情')
  })

  it('shows an empty state when the market has no packages', async () => {
    mocks.fetchRepositoryContents.mockResolvedValue({ entries: [], directPlugin: null })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('暂无插件')
    expect(wrapper.text()).toContain('下一步')
    expect(wrapper.find('a[href="/plugins/repositories"]').exists()).toBe(true)
  })

  it('shows signed package facts and confirms permissions through a modal', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('WAF')
    expect(wrapper.text()).toContain('未安装')
    await openFirstPackage(wrapper)

    expect(wrapper.text()).toContain('非官方来源')
    expect(wrapper.text()).toContain('未安装')
    expect(wrapper.text()).toContain('安装后把插件部署到一个节点即可在该节点上使用。')
    expect(nextStep(wrapper)).toContain('下一步：部署到一个节点')
    expect(nextStep(wrapper)).not.toContain('发布域名')
    expect(buttonByText(wrapper, '安装插件')).toBeTruthy()
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

    // Inline checkbox gating is replaced by a confirmation modal.
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)

    const install = buttonByText(wrapper, '安装插件')
    expect(install).toBeTruthy()
    expect(install.attributes('disabled')).toBeUndefined()

    await install.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-stub').exists()).toBe(true)
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    expect(wrapper.find('.modal-stub').text()).toContain('http.inspect')
    expect(wrapper.find('.modal-stub').text()).toContain('我已复核非官方来源')
    expect(wrapper.find('[data-test="marketplace-confirm-next"]').text()).toContain('下一步：部署到一个节点')

    const confirm = buttonByText(wrapper, '确认安装')
    await confirm.trigger('click')
    await flushPromises()

    expect(mocks.installPlugin).toHaveBeenCalledWith(expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
    expect(wrapper.find('.modal-stub').exists()).toBe(false)
  })

  it('confirms an upgrade through the same modal with the upgrade payload', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_version: '1.1.0', active_package_digest: 'c'.repeat(64) }])
    const wrapper = mountPage()
    await flushPromises()
    await openFirstPackage(wrapper)

    const upgrade = buttonByText(wrapper, '升级插件')
    expect(upgrade).toBeTruthy()
    expect(nextStep(wrapper)).toContain('升级后会进入详情')
    expect(nextStep(wrapper)).toContain('下一步：部署到一个节点')
    expect(nextStep(wrapper)).not.toContain('发布域名')
    await upgrade.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认升级插件')
    expect(wrapper.find('[data-test="marketplace-confirm-next"]').text()).toContain('下一步：部署到一个节点')

    const confirm = buttonByText(wrapper, '确认升级')
    await confirm.trigger('click')
    await flushPromises()

    expect(mocks.upgradePlugin).toHaveBeenCalledWith(entry.id, expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
    expect(mocks.push).toHaveBeenCalledWith(`/plugins/${encodeURIComponent(entry.id)}`)
  })

  it('disables install and shows the installed notice when the current digest is already present', async () => {
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_version: entry.version, active_package_digest: entry.sha256 }])
    const wrapper = mountPage()
    await flushPromises()
    await openFirstPackage(wrapper)

    expect(wrapper.text()).toContain('当前版本已安装')
    expect(nextStep(wrapper)).toContain('打开详情继续部署')
    expect(wrapper.find(`a[href="/plugins/${encodeURIComponent(entry.id)}"]`).exists()).toBe(true)
    const install = buttonByText(wrapper, '安装插件')
    expect(install.attributes('disabled')).toBeDefined()
    await install.trigger('click')
    expect(wrapper.find('.modal-title').text()).not.toBe('确认安装插件')
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
    expect(wrapper.get(`[data-test="marketplace-card-action-${entry.id}"]`).attributes('title')).toBe('打开详情')
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
    await openFirstPackage(wrapper)

    expect(wrapper.text()).toContain('把站点流量交给这个插件处理。')
    expect(nextStep(wrapper)).toContain('发布')
    expect(nextStep(wrapper)).toContain('入口域名')

    await buttonByText(wrapper, '安装插件').trigger('click')
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
    await openFirstPackage(wrapper)

    expect(nextStep(wrapper)).toContain('打开详情继续部署')
    expect(nextStep(wrapper)).toContain('发布域名')
    expect(wrapper.find(`a[href="/plugins/${encodeURIComponent(entry.id)}"]`).text()).toContain('打开详情')
  })

  it('returns to package inspect when canceling confirm opened from details', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await openFirstPackage(wrapper)
    expect(wrapper.find('.modal-title').text()).toBe('WAF')
    await buttonByText(wrapper, '安装插件').trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
    await buttonByText(wrapper, '取消').trigger('click')
    expect(wrapper.find('.modal-title').text()).toBe('WAF')
  })

  it('stays on the market and does not navigate when install fails', async () => {
    mocks.installPlugin.mockRejectedValue(new Error('source rejected'))
    const wrapper = mountPage()
    await flushPromises()
    await openFirstPackage(wrapper)
    await buttonByText(wrapper, '安装插件').trigger('click')
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
    mocks.fetchPlugins.mockResolvedValue([{ plugin_id: entry.id, active_package_digest: 'c'.repeat(64) }])
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

  it('switches the catalog to a list table', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.find('.plugin-marketplace-catalog').exists()).toBe(true)
    await wrapper.get('button[title="列表视图"]').trigger('click')
    expect(wrapper.find('.plugin-marketplace-catalog').exists()).toBe(false)
    const table = wrapper.get('[data-test="marketplace-table"]')
    expect(table.text()).toContain('WAF')
    expect(table.text()).toContain('安装')
    const action = table.get(`[data-test="marketplace-card-action-${entry.id}"]`)
    expect(action.classes()).toContain('btn-sm')
    expect(action.classes()).toContain('btn-primary')
    await action.trigger('click')
    await flushPromises()
    expect(wrapper.find('.modal-title').text()).toBe('确认安装插件')
  })
})
