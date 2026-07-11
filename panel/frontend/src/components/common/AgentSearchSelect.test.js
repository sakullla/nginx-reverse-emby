import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AgentSearchSelect from './AgentSearchSelect.vue'
import { ALL_AGENTS_FILTER } from '../../utils/agentFilter.js'

const agents = [
  { id: 'local', name: '本机', status: 'online' },
  { id: 'edge-1', name: 'Edge One', status: 'online' },
  { id: 'edge-2', name: 'Edge Two', status: 'offline' }
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

  it('filters agents by name and id, and shows empty state when none match', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.agent-search-select__trigger').trigger('click')

    const input = wrapper.find('.agent-search-select__search-input')
    await input.setValue('edge')
    expect(wrapper.findAll('.agent-search-select__option')).toHaveLength(3) // all + 2 edges
    expect(wrapper.text()).toContain('Edge One')
    expect(wrapper.text()).toContain('Edge Two')
    expect(wrapper.text()).not.toContain('本机')

    await input.setValue('edge-2')
    expect(wrapper.text()).toContain('Edge Two')
    expect(wrapper.text()).not.toContain('Edge One')

    await input.setValue('no-such-agent')
    expect(wrapper.find('.agent-search-select__empty').exists()).toBe(true)
    expect(wrapper.text()).toContain('没有匹配的节点')
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
    // no multi-select: only one value emitted per click
    expect(emitted?.[0]).toHaveLength(1)
  })
})
