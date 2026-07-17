import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AgentSearchSelect from './AgentSearchSelect.vue'
import { ALL_AGENTS_FILTER } from '../../utils/agentFilter.js'

const agents = [
  { id: 'local', name: '本机', status: 'online', last_seen_at: new Date(Date.now() - 30_000).toISOString() },
  { id: 'edge-1', name: 'Edge One', status: 'online', last_seen_at: new Date(Date.now() - 120_000).toISOString() },
  { id: 'edge-2', name: 'Edge Two', status: 'offline', last_seen_at: new Date(Date.now() - 86_400_000).toISOString() }
]

function mountSelect(props = {}) {
  return mount(AgentSearchSelect, {
    props: {
      agents,
      modelValue: ALL_AGENTS_FILTER,
      ...props
    }
  })
}

describe('AgentSearchSelect', () => {
  it('shows all-agents label by default and emits ALL_AGENTS_FILTER', async () => {
    const wrapper = mountSelect()
    expect(wrapper.text()).toContain('全部节点')

    await wrapper.find('.agent-search-select__trigger').trigger('click')
    const allOption = wrapper.findAll('.agent-search-select__option')[0]
    await allOption.trigger('click')

    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([ALL_AGENTS_FILTER])
  })

  it('does not display agent ids in the dropdown or trigger', async () => {
    const wrapper = mountSelect({ modelValue: 'edge-1' })
    expect(wrapper.text()).toContain('Edge One')
    expect(wrapper.text()).not.toContain('edge-1')

    await wrapper.find('.agent-search-select__trigger').trigger('click')
    expect(wrapper.text()).toContain('Edge One')
    expect(wrapper.text()).not.toContain('edge-1')
    expect(wrapper.text()).not.toContain('edge-2')
    expect(wrapper.find('.agent-search-select__option-id').exists()).toBe(false)
  })

  it('filters agents by name only and shows empty state when none match', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.agent-search-select__trigger').trigger('click')

    const input = wrapper.find('.agent-search-select__search-input')
    await input.setValue('edge')
    expect(wrapper.findAll('.agent-search-select__option')).toHaveLength(3) // all + 2 edges
    expect(wrapper.text()).toContain('Edge One')
    expect(wrapper.text()).toContain('Edge Two')
    expect(wrapper.text()).not.toContain('本机')

    await input.setValue('no-such-agent')
    expect(wrapper.find('.agent-search-select__empty').exists()).toBe(true)
    expect(wrapper.text()).toContain('没有匹配的节点')
  })

  it('shows a search clear button only when query is non-empty and clears it', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.agent-search-select__trigger').trigger('click')

    expect(wrapper.find('.agent-search-select__search-clear').exists()).toBe(false)

    const input = wrapper.find('.agent-search-select__search-input')
    await input.setValue('edge')
    const clear = wrapper.find('.agent-search-select__search-clear')
    expect(clear.exists()).toBe(true)
    expect(clear.attributes('aria-label')).toBe('清空搜索')

    await clear.trigger('click')
    expect(input.element.value).toBe('')
    expect(wrapper.find('.agent-search-select__search-clear').exists()).toBe(false)
    expect(wrapper.text()).toContain('本机')
  })

  it('shows last seen time in options instead of id', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.agent-search-select__trigger').trigger('click')
    expect(wrapper.find('.agent-search-select__option-time').exists()).toBe(true)
    expect(wrapper.text()).toContain('刚刚')
  })

  it('filters by online/offline status', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.agent-search-select__trigger').trigger('click')

    await wrapper.find('.agent-search-select__filter-btn--offline').trigger('click')
    expect(wrapper.text()).toContain('Edge Two')
    expect(wrapper.text()).not.toContain('Edge One')
    expect(wrapper.text()).not.toContain('本机')

    await wrapper.find('.agent-search-select__filter-btn--online').trigger('click')
    expect(wrapper.text()).toContain('Edge One')
    expect(wrapper.text()).toContain('本机')
    expect(wrapper.text()).not.toContain('Edge Two')
  })

  it('sorts by name when sort-by-name is selected', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.agent-search-select__trigger').trigger('click')

    await wrapper.find('.agent-search-select__sort-btn--name').trigger('click')
    const names = wrapper.findAll('.agent-search-select__option-name')
      .map((node) => node.text())
    expect(names).toEqual(['全部节点', '本机', 'Edge One', 'Edge Two'])
  })

  it('emits concrete agent id on select and is single-select only', async () => {
    const wrapper = mountSelect({ modelValue: 'local' })
    expect(wrapper.text()).toContain('本机')

    await wrapper.find('.agent-search-select__trigger').trigger('click')
    const edgeOne = wrapper.findAll('.agent-search-select__option')
      .find((node) => node.text().includes('Edge One'))
    await edgeOne.trigger('click')

    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted?.[0]?.[0]).toBe('edge-1')
    expect(emitted?.[0]).toHaveLength(1)
  })
})
