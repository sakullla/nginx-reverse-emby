import { describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import SettingsDataMgmt from './SettingsDataMgmt.vue'

vi.mock('../../api', () => ({
  fetchBackupResourceCounts: vi.fn(() => Promise.resolve({
    counts: {
      agents: 1,
      http_rules: 2,
      l4_rules: 0,
      relay_listeners: 1,
      certificates: 0,
      version_policies: 1
    }
  }))
}))

describe('SettingsDataMgmt', () => {
  it('renders page title and tabbed import/export interface', async () => {
    const wrapper = mount(SettingsDataMgmt)
    await flushPromises()

    expect(wrapper.text()).toContain('数据管理')
    expect(wrapper.text()).toContain('导出或导入面板配置')
    expect(wrapper.find('[aria-controls="panel-export"]').text()).toContain('导出配置')
    expect(wrapper.find('[aria-controls="panel-import"]').text()).toContain('导入配置')
  })

  it('shows export panel by default', async () => {
    const wrapper = mount(SettingsDataMgmt)
    await flushPromises()

    const exportPanel = wrapper.find('#panel-export')
    expect(exportPanel.exists()).toBe(true)
    expect(exportPanel.attributes('hidden')).toBeUndefined()
    expect(exportPanel.text()).toContain('导出配置')

    const importPanel = wrapper.find('#panel-import')
    expect(importPanel.attributes('hidden')).toBeDefined()
  })

  it('switches to import panel when import tab is clicked', async () => {
    const wrapper = mount(SettingsDataMgmt)
    await flushPromises()

    const importTab = wrapper.find('[aria-controls="panel-import"]')
    await importTab.trigger('click')
    await wrapper.vm.$nextTick()

    expect(importTab.classes()).toContain('active')

    const exportPanel = wrapper.find('#panel-export')
    expect(exportPanel.attributes('hidden')).toBeDefined()

    const importPanel = wrapper.find('#panel-import')
    expect(importPanel.attributes('hidden')).toBeUndefined()
    expect(importPanel.text()).toContain('导入配置')
  })
})
