import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginRepositoriesPage from './PluginRepositoriesPage.vue'

const {
  fetchRepositorySources,
  createRepositorySource,
  updateRepositorySource,
  deleteRepositorySource,
  refreshRepositorySource,
  fetchRepositoryContents
} = vi.hoisted(() => ({
  fetchRepositorySources: vi.fn(),
  createRepositorySource: vi.fn(),
  updateRepositorySource: vi.fn(),
  deleteRepositorySource: vi.fn(),
  refreshRepositorySource: vi.fn(),
  fetchRepositoryContents: vi.fn()
}))

vi.mock('../../api/pluginRepositories', () => ({
  fetchRepositorySources,
  createRepositorySource,
  updateRepositorySource,
  deleteRepositorySource,
  refreshRepositorySource,
  fetchRepositoryContents
}))

const customSource = {
  id: 'team-plugins',
  kind: 'custom',
  purpose: 'plugin',
  name: 'Team Plugins',
  url: 'https://git.example.com/team/plugins.git',
  ref_kind: 'tag',
  ref_name: 'v1.2.3',
  credential_configured: true,
  signer_key_id: 'team-release',
  signer_fingerprint: 'SHA256:team',
  refresh_interval_ns: 3600000000000,
  risk_label: 'custom-review-required',
  config_revision: 3,
  current_resolved_oid: '0123456789abcdef0123456789abcdef01234567',
  current_snapshot: 'snapshot-3',
  last_result: 'succeeded',
  last_error: '',
  last_completed_at: '2026-08-10T08:00:00Z'
}

const officialSource = {
  ...customSource,
  id: 'official-market',
  kind: 'official',
  purpose: 'market',
  name: 'Official Market',
  risk_label: 'official',
  ref_kind: 'branch',
  ref_name: 'main'
}

let wrapper

function buttonByText(text) {
  return wrapper.findAll('button').find((button) => button.text().includes(text))
}

beforeEach(() => {
  fetchRepositorySources.mockReset().mockResolvedValue([customSource, officialSource])
  createRepositorySource.mockReset().mockResolvedValue(customSource)
  updateRepositorySource.mockReset().mockResolvedValue(customSource)
  deleteRepositorySource.mockReset().mockResolvedValue({ ok: true })
  refreshRepositorySource.mockReset().mockResolvedValue({ source_id: customSource.id, commit: customSource.current_resolved_oid })
  fetchRepositoryContents.mockReset().mockImplementation(async (id) => id === customSource.id
    ? { entries: [], directPlugin: { id: 'team.waf', version: '1.2.3', runtime: { kind: 'wasm-policy' }, sha256: 'a'.repeat(64) } }
    : { entries: [{ id: 'official.waf', version: '1.0.0', runtime: { kind: 'wasm-policy' }, sha256: 'b'.repeat(64) }], directPlugin: null })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

async function mountPage() {
  wrapper = mount(PluginRepositoriesPage)
  await flushPromises()
  return wrapper
}

describe('PluginRepositoriesPage', () => {
  it('shows purpose, configured ref, full resolved OID, provenance risk and current state', async () => {
    await mountPage()

    expect(wrapper.text()).toContain('插件包')
    expect(wrapper.text()).toContain('标签 / v1.2.3')
    expect(wrapper.text()).toContain(customSource.current_resolved_oid)
    expect(wrapper.text()).toContain('自定义来源 · custom-review-required')
    expect(wrapper.text()).toContain('当前可用')
    expect(wrapper.text()).toContain('Git 凭据')
    expect(wrapper.text()).toContain('已配置')
    expect(wrapper.text()).toContain('team.waf')
    expect(wrapper.text()).toContain('wasm-policy')
  })

  it('surfaces the durable refresh error in both status and detail', async () => {
    fetchRepositorySources.mockResolvedValueOnce([{
      ...customSource,
      current_resolved_oid: '',
      current_snapshot: '',
      last_result: 'failed',
      last_error: 'credential rejected'
    }])
    await mountPage()

    expect(wrapper.text()).toContain('刷新失败')
    expect(wrapper.text()).toContain('最近刷新失败：credential rejected')
  })

  it('keeps official sources immutable in the UI while allowing refresh', async () => {
    await mountPage()
    await buttonByText('Official Market').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('官方来源 · official')
    expect(buttonByText('编辑')).toBeUndefined()
    expect(buttonByText('删除源')).toBeUndefined()
    expect(wrapper.text()).toContain('official.waf')

    await buttonByText('立即刷新').trigger('click')
    await flushPromises()
    expect(refreshRepositorySource).toHaveBeenCalledWith('official-market')
  })

  it('states that deleting a source does not uninstall plugins', async () => {
    await mountPage()
    await buttonByText('删除源').trigger('click')

    expect(wrapper.get('[role="alertdialog"]').text()).toContain('不会卸载已经安装的插件')
    await buttonByText('确认删除源').trigger('click')
    await flushPromises()

    expect(deleteRepositorySource).toHaveBeenCalledWith('team-plugins')
  })

  it('routes create and edit form payloads through the stable API contract', async () => {
    await mountPage()
    await buttonByText('新增仓库源').trigger('click')
    const createPayload = {
      id: 'market-two',
      name: 'Market Two',
      url: 'https://git.example.com/market-two.git',
      purpose: 'market',
      ref_kind: 'branch',
      ref_name: 'main',
      signer_key_id: '',
      refresh_interval: ''
    }
    wrapper.findComponent({ name: 'RepositorySourceForm' }).vm.$emit('save', createPayload)
    await flushPromises()
    expect(createRepositorySource).toHaveBeenCalledWith(createPayload)

    await buttonByText('编辑').trigger('click')
    const editPayload = { name: 'Renamed source', ref_name: 'v1.2.4' }
    wrapper.findComponent({ name: 'RepositorySourceForm' }).vm.$emit('save', editPayload)
    await flushPromises()
    expect(updateRepositorySource).toHaveBeenCalledWith('team-plugins', editPayload)
  })
})
