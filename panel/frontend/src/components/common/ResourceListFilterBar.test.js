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
    }
  })
}

describe('ResourceListFilterBar', () => {
  it('renders agent select, search input, and configured status field', () => {
    const wrapper = mountBar()
    expect(wrapper.find('.agent-search-select').exists()).toBe(true)
    expect(wrapper.find('.resource-list-filter-bar__input').exists()).toBe(true)
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

  it('emits status updates for configured fields and can hide search', async () => {
    const wrapper = mountBar({ showSearch: false })
    expect(wrapper.find('.resource-list-filter-bar__input').exists()).toBe(false)

    const select = wrapper.find('.resource-list-filter-bar__select')
    await select.setValue('true')
    expect(wrapper.emitted('update:status')?.[0]).toEqual([{ key: 'enabled', value: 'true' }])
  })
})
