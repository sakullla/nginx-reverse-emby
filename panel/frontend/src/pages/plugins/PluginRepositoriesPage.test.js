import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginRepositoriesPage from './PluginRepositoriesPage.vue'
import { messageStore } from '../../stores/messages'

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

vi.mock('../../components/DeleteConfirmDialog.vue', () => ({
  default: {
    name: 'DeleteConfirmDialog',
    props: ['show', 'title', 'message', 'name', 'confirmText', 'loading'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" class="delete-dialog-stub"><div class="delete-dialog-title">{{ title }}</div><div class="delete-dialog-message">{{ message }}</div><div class="delete-dialog-name">{{ name }}</div><button class="delete-dialog-confirm" @click="$emit(\'confirm\')">{{ confirmText }}</button><button class="delete-dialog-cancel" @click="$emit(\'cancel\')">取消</button></div>'
  }
}))

vi.mock('../../components/base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: {
      modelValue: { type: Boolean, required: true },
      title: { type: String, default: '' },
      subtitle: { type: String, default: '' },
      size: { type: String, default: 'md' },
      showFooter: { type: Boolean, default: false }
    },
    emits: ['update:modelValue'],
    template: '<div v-if="modelValue" class="modal-stub"><div class="modal-title">{{ title }}</div><div class="modal-body"><slot /></div><div v-if="showFooter" class="modal-footer"><slot name="footer" /></div></div>'
  }
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

async function openSource(name) {
  const card = wrapper.findAll('.repository-card').find((item) => item.text().includes(name))
  expect(card).toBeTruthy()
  await card.trigger('click')
  await flushPromises()
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
  messageStore.clearAll()
})

async function mountPage() {
  wrapper = mount(PluginRepositoriesPage, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
  })
  await flushPromises()
  return wrapper
}

describe('PluginRepositoriesPage', () => {
  it('renders the shared page header with a back link and primary action', async () => {
    await mountPage()
    expect(wrapper.find('.page-title').text()).toBe('插件仓库')
    expect(wrapper.find('.repository-advanced-label').text()).toContain('高级入口')
    expect(wrapper.find('.page-subtitle').text()).toContain('最近刷新')
    expect(wrapper.find('.page-subtitle').text()).toContain('不是安装或发布的起点')
    expect(wrapper.find('.back-link').attributes('href')).toBe('/plugins/marketplace')
    expect(wrapper.find('.page-header__right a').attributes('href')).toBe('/plugins/marketplace')
    expect(wrapper.find('.page-header__right a').text()).toContain('去市场安装')
    expect(buttonByText('新增仓库源').classes()).toContain('btn-primary')
  })

  it('uses next-step empty copy and keeps the market as the install path', async () => {
    fetchRepositorySources.mockResolvedValueOnce([])
    await mountPage()

    expect(wrapper.text()).toContain('还没有仓库源')
    expect(wrapper.text()).toContain('下一步')
    expect(wrapper.text()).toContain('请先去市场')
    expect(wrapper.findAll('a').some((link) => link.attributes('href') === '/plugins/marketplace')).toBe(true)
    expect(buttonByText('新增仓库源').exists()).toBe(true)
  })

  it('shows purpose, configured ref, full resolved OID, provenance risk and current state', async () => {
    await mountPage()
    await openSource('Team Plugins')

    expect(wrapper.text()).toContain('插件包')
    expect(wrapper.text()).toContain('当前可用')
    expect(wrapper.find('.repository-technical').exists()).toBe(true)
    expect(wrapper.find('.repository-technical').element.open).toBeFalsy()
    expect(wrapper.text()).toContain('标签 / v1.2.3')
    expect(wrapper.text()).toContain(customSource.current_resolved_oid)
    expect(wrapper.text()).toContain('自定义来源 · custom-review-required')
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
    await openSource('Team Plugins')

    expect(wrapper.text()).toContain('刷新失败')
    expect(wrapper.text()).toContain('最近刷新失败：credential rejected')
  })

  it('shows preview catalog names when sources fail to load', async () => {
    fetchRepositorySources.mockRejectedValue(new Error('backend unavailable'))
    await mountPage()

    expect(wrapper.text()).toContain('官方市场')
    expect(wrapper.text()).toContain('团队插件仓库')
  })

  it('routes action errors through the unified alert channel', async () => {
    refreshRepositorySource.mockRejectedValue(new Error('credential rejected'))
    await mountPage()
    await openSource('Team Plugins')
    await buttonByText('立即刷新').trigger('click')
    await flushPromises()

    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(messageStore.state.messages.map((item) => item.text).join('\n')).toContain('credential rejected')
  })

  it('keeps the create form visible when saving the first source fails', async () => {
    fetchRepositorySources.mockResolvedValueOnce([])
    createRepositorySource.mockRejectedValue(new Error('duplicate id'))
    await mountPage()

    await buttonByText('新增仓库源').trigger('click')
    expect(wrapper.findComponent({ name: 'RepositorySourceForm' }).exists()).toBe(true)

    wrapper.findComponent({ name: 'RepositorySourceForm' }).vm.$emit('save', {
      id: 'market-two',
      name: 'Market Two',
      url: 'https://git.example.com/market-two.git',
      purpose: 'market',
      ref_kind: 'branch',
      ref_name: 'main',
      signer_key_id: '',
      refresh_interval: ''
    })
    await flushPromises()

    expect(wrapper.findComponent({ name: 'RepositorySourceForm' }).exists()).toBe(true)
    expect(wrapper.text()).not.toContain('duplicate id')
    expect(messageStore.state.messages.map((item) => item.text).join('\n')).toContain('duplicate id')
    expect(wrapper.text()).not.toContain('读取失败')
    expect(createRepositorySource).toHaveBeenCalled()
  })

  it('shows a distinct placeholder when package contents fail to load', async () => {
    fetchRepositoryContents.mockRejectedValue(new Error('snapshot unavailable'))
    await mountPage()
    await openSource('Team Plugins')

    expect(wrapper.text()).not.toContain('snapshot unavailable')
    expect(messageStore.state.messages.map((item) => item.text).join('\n')).toContain('snapshot unavailable')
    expect(wrapper.text()).toContain('读取包投影失败')
    expect(wrapper.text()).not.toContain('当前快照没有可用包')
  })

  it('keeps official sources immutable in the UI while allowing refresh', async () => {
    await mountPage()
    await openSource('Official Market')

    expect(wrapper.text()).toContain('官方来源 · official')
    expect(buttonByText('编辑')).toBeUndefined()
    expect(buttonByText('删除源')).toBeUndefined()
    expect(wrapper.text()).toContain('official.waf')
    expect(buttonByText('关闭').classes()).toEqual(expect.arrayContaining(['btn-ghost', 'btn-sm']))
    expect(buttonByText('立即刷新').classes()).toEqual(expect.arrayContaining(['btn-primary', 'btn-sm']))
    expect(wrapper.find('.repository-package-list').exists()).toBe(true)

    await buttonByText('立即刷新').trigger('click')
    await flushPromises()
    expect(refreshRepositorySource).toHaveBeenCalledWith('official-market')
  })

  it('confirms deletion through DeleteConfirmDialog without uninstalling plugins', async () => {
    await mountPage()
    await openSource('Team Plugins')
    await buttonByText('删除源').trigger('click')

    expect(wrapper.find('.repository-detail__notice').text()).toContain('不会卸载已经安装的插件')
    expect(wrapper.find('.repository-detail__notice').text()).toContain('停止从该 Git 来源继续发现或刷新内容')
    expect(wrapper.find('.repository-detail__notice a').attributes('href')).toBe('/plugins')
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)
    expect(wrapper.find('.delete-dialog-message').text()).toContain('不会卸载已经安装的插件')
    expect(wrapper.find('.delete-dialog-message').text()).toContain('将停止从该仓库源继续发现和刷新内容')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()

    expect(deleteRepositorySource).toHaveBeenCalledWith('team-plugins')
  })

  it('routes create and edit form payloads through the stable API contract', async () => {
    await mountPage()
    await openSource('Team Plugins')
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
    expect(updateRepositorySource).toHaveBeenCalledWith('team-plugins', { ...editPayload, config_revision: 3 })
  })
})
