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

beforeEach(() => {
  mocks.fetchRepositorySources.mockReset().mockResolvedValue([source])
  mocks.fetchRepositoryContents.mockReset().mockResolvedValue({ entries: [entry], directPlugin: null })
  mocks.fetchPlugins.mockReset().mockResolvedValue([])
  mocks.fetchPluginPackageDetail.mockReset().mockResolvedValue(packageDetail)
  mocks.installPlugin.mockReset().mockResolvedValue({ plugin_id: entry.id })
  mocks.upgradePlugin.mockReset()
})

describe('PluginMarketplacePage', () => {
  it('shows signed package/runtime/risk facts and requires exact confirmations', async () => {
    const wrapper = mount(PluginMarketplacePage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    expect(wrapper.text()).toMatch(/非官方来源|wasm-policy|nre:policy\/v1|Package SHA-256|权限差异|WAF fail-closed|不能注入宿主前端代码/)
    expect(wrapper.text()).not.toContain('packageCode')
    const submit = wrapper.findAll('button').find((button) => button.text().includes('确认权限并安装'))
    expect(submit.attributes('disabled')).toBeDefined()
    const checks = wrapper.findAll('input[type="checkbox"]')
    await checks[0].setValue(true)
    await checks[1].setValue(true)
    await submit.trigger('click')
    await flushPromises()

    expect(mocks.installPlugin).toHaveBeenCalledWith(expect.objectContaining({
      source_id: 'community', plugin_id: entry.id, digest: entry.sha256,
      confirmed_permissions: ['http.inspect'], risk_accepted: true
    }))
  })
})
