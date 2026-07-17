import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ResourceListFilterBar from './ResourceListFilterBar.vue'
import { ALL_AGENTS_FILTER } from '../../utils/agentFilter.js'

const agents = [
  { id: 'local', name: '本机', status: 'online' },
  { id: 'edge', name: 'Edge', status: 'online' }
]

const enabledField = {
  key: 'enabled',
  label: '启用状态',
  options: [
    { value: '', label: '全部' },
    { value: 'true', label: '启用' },
    { value: 'false', label: '停用' }
  ]
}

function mountBar(props = {}) {
  return mount(ResourceListFilterBar, {
    props: {
      agentId: ALL_AGENTS_FILTER,
      agents,
      q: '',
      showSearch: true,
      statusFields: [enabledField],
      statusValues: { enabled: '' },
      ...props
    },
    attachTo: document.body
  })
}

describe('ResourceListFilterBar', () => {
  it('keeps agent and search on the main bar and hides status until filter opens', async () => {
    const wrapper = mountBar()
    expect(wrapper.find('.agent-search-select').exists()).toBe(true)
    expect(wrapper.find('.resource-list-filter-bar__input').exists()).toBe(true)
    expect(wrapper.find('.resource-list-filter-bar__filter-trigger').exists()).toBe(true)
    expect(wrapper.find('.resource-list-filter-bar__panel').exists()).toBe(false)
    expect(wrapper.find('.resource-list-filter-bar__select').exists()).toBe(false)

    await wrapper.find('.resource-list-filter-bar__filter-trigger').trigger('click')
    expect(wrapper.find('.resource-list-filter-bar__panel').exists()).toBe(true)
    expect(wrapper.find('.resource-list-filter-bar__select').exists()).toBe(true)
    expect(wrapper.find('option[value="true"]').exists()).toBe(true)
  })

  it('forwards agent and search updates', async () => {
    const wrapper = mountBar()

    await wrapper.find('.agent-search-select__trigger').trigger('click')
    const localOption = wrapper.findAll('.agent-search-select__option')
      .find((node) => node.text().includes('本机'))
    await localOption.trigger('click')
    expect(wrapper.emitted('update:agentId')?.[0]).toEqual(['local'])

    await wrapper.find('.resource-list-filter-bar__input').setValue('emby')
    expect(wrapper.emitted('update:q')?.[0]).toEqual(['emby'])
  })

  it('shows a search clear button only when query is non-empty and clears it', async () => {
    const empty = mountBar({ q: '' })
    expect(empty.find('.resource-list-filter-bar__clear').exists()).toBe(false)

    const wrapper = mountBar({ q: 'emby' })
    const clear = wrapper.find('.resource-list-filter-bar__clear')
    expect(clear.exists()).toBe(true)
    expect(clear.attributes('aria-label')).toBe('清空搜索')

    await clear.trigger('click')
    expect(wrapper.emitted('update:q')?.[0]).toEqual([''])
    expect(wrapper.emitted('change')?.[0]).toEqual([{ type: 'q', value: '' }])
  })

  it('emits status updates from the filter panel and can hide search', async () => {
    const wrapper = mountBar({ showSearch: false })
    expect(wrapper.find('.resource-list-filter-bar__input').exists()).toBe(false)

    await wrapper.find('.resource-list-filter-bar__filter-trigger').trigger('click')
    const select = wrapper.find('.resource-list-filter-bar__select')
    await select.setValue('true')
    expect(wrapper.emitted('update:status')?.[0]).toEqual([{ key: 'enabled', value: 'true' }])
  })

  it('shows an active badge when status differs from default and can reset', async () => {
    const wrapper = mountBar({ statusValues: { enabled: 'true' } })
    const badge = wrapper.find('.resource-list-filter-bar__filter-badge')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('1')

    await wrapper.find('.resource-list-filter-bar__filter-trigger').trigger('click')
    await wrapper.find('.resource-list-filter-bar__reset').trigger('click')
    expect(wrapper.emitted('update:status')).toEqual([
      [{ key: 'enabled', value: '' }]
    ])
  })

  it('hides the filter trigger when there are no status fields', () => {
    const wrapper = mountBar({ statusFields: [] })
    expect(wrapper.find('.resource-list-filter-bar__filter-trigger').exists()).toBe(false)
  })
})
