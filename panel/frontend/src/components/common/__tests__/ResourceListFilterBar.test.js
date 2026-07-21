import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ResourceListFilterBar from '../ResourceListFilterBar.vue'

const AGENTS = [
  { id: 1, name: 'master' },
  { id: 2, name: 'edge-1' }
]

const FILTER_FIELDS = [
  {
    key: 'enabled',
    label: '启用状态',
    type: 'chip',
    options: [
      { value: '', label: '全部' },
      { value: '1', label: '启用' },
      { value: '0', label: '停用' }
    ]
  },
  {
    key: 'sync',
    label: '同步状态',
    type: 'chip',
    options: [
      { value: '', label: '全部' },
      { value: 'applied', label: '已同步' },
      { value: 'pending', label: '待同步' }
    ]
  },
  {
    key: 'certificate_id',
    label: '证书',
    type: 'select',
    options: [
      { value: '', label: '全部' },
      { value: '7', label: 'example.com' }
    ]
  },
  {
    key: 'tags',
    label: '标签',
    type: 'multi',
    options: [
      { value: 'emby', label: 'emby' },
      { value: 'web', label: 'web' }
    ]
  }
]

function mountBar(props = {}) {
  return mount(ResourceListFilterBar, {
    props: {
      agentId: '',
      agents: AGENTS,
      q: '',
      filterFields: FILTER_FIELDS,
      filterValues: {},
      ...props
    },
    global: {
      stubs: {
        AgentSearchSelect: {
          props: ['modelValue', 'agents'],
          template: '<div class="agent-search-select-stub" />'
        }
      }
    }
  })
}

describe('ResourceListFilterBar', () => {
  it('renders chip fields in the quick chip row and keeps panel fields out of it', () => {
    const wrapper = mountBar()
    const chips = wrapper.findAll('.resource-list-filter-bar__chips .resource-list-filter-bar__chip')
    expect(chips.map((chip) => chip.text())).toEqual(['启用', '停用', '已同步', '待同步'])
    expect(wrapper.find('.resource-list-filter-bar__filter-trigger').exists()).toBe(true)
  })

  it('hides the filter trigger when no panel fields exist', () => {
    const wrapper = mountBar({ filterFields: FILTER_FIELDS.filter((f) => f.type === 'chip') })
    expect(wrapper.find('.resource-list-filter-bar__filter-trigger').exists()).toBe(false)
  })

  it('toggles a chip on and emits the option value, then back to baseline', async () => {
    const wrapper = mountBar()
    const chip = wrapper.findAll('.resource-list-filter-bar__chips .resource-list-filter-bar__chip')[0]
    await chip.trigger('click')
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'enabled', value: '1' }]])

    await wrapper.setProps({ filterValues: { enabled: '1' } })
    await chip.trigger('click')
    expect(wrapper.emitted('update:filter')[1]).toEqual([{ key: 'enabled', value: '' }])
  })

  it('marks the active chip with aria-pressed and active class', async () => {
    const wrapper = mountBar({ filterValues: { enabled: '0' } })
    const chips = wrapper.findAll('.resource-list-filter-bar__chips .resource-list-filter-bar__chip')
    expect(chips[1].classes()).toContain('resource-list-filter-bar__chip--active')
    expect(chips[1].attributes('aria-pressed')).toBe('true')
    expect(chips[0].attributes('aria-pressed')).toBe('false')
  })

  it('toggles multi options inside the panel and emits arrays', async () => {
    const wrapper = mountBar({ filterValues: { tags: ['emby'] } })
    await wrapper.find('.resource-list-filter-bar__filter-trigger').trigger('click')
    const multiChips = wrapper.findAll('.resource-list-filter-bar__multi .resource-list-filter-bar__chip')
    expect(multiChips).toHaveLength(2)
    expect(multiChips[0].classes()).toContain('resource-list-filter-bar__chip--active')

    await multiChips[1].trigger('click')
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'tags', value: ['emby', 'web'] }]])

    await wrapper.setProps({ filterValues: { tags: ['emby', 'web'] } })
    await multiChips[0].trigger('click')
    expect(wrapper.emitted('update:filter')[1]).toEqual([{ key: 'tags', value: ['web'] }])
  })

  it('emits select changes from the panel', async () => {
    const wrapper = mountBar()
    await wrapper.find('.resource-list-filter-bar__filter-trigger').trigger('click')
    const select = wrapper.find('.resource-list-filter-bar__select')
    await select.setValue('7')
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'certificate_id', value: '7' }]])
  })

  it('renders one condition tag per active filter with resolved option labels', () => {
    const wrapper = mountBar({
      agentId: '2',
      agentBaseline: '',
      filterValues: { enabled: '1', tags: ['emby', 'web'] }
    })
    const tags = wrapper.findAll('.resource-list-filter-bar__condition')
    expect(tags).toHaveLength(3)
    expect(tags[0].text()).toContain('节点: edge-1')
    expect(tags[1].text()).toContain('启用状态: 启用')
    expect(tags[2].text()).toContain('标签: emby、web')
  })

  it('removes a condition tag by emitting its baseline', async () => {
    const wrapper = mountBar({ filterValues: { enabled: '1', tags: ['emby'] } })
    const tags = wrapper.findAll('.resource-list-filter-bar__condition')
    await tags[0].trigger('click')
    expect(wrapper.emitted('update:filter')).toEqual([[{ key: 'enabled', value: '' }]])
    await tags[1].trigger('click')
    expect(wrapper.emitted('update:filter')[1]).toEqual([{ key: 'tags', value: [] }])
  })

  it('removes the agent condition tag by emitting the agent baseline', async () => {
    const wrapper = mountBar({ agentId: '2', agentBaseline: '__all__' })
    await wrapper.find('.resource-list-filter-bar__condition').trigger('click')
    expect(wrapper.emitted('update:agentId')).toEqual([['__all__']])
  })

  it('does not show the agent condition tag at baseline', () => {
    const wrapper = mountBar({ agentId: '__all__', agentBaseline: '__all__' })
    expect(wrapper.find('.resource-list-filter-bar__condition').exists()).toBe(false)
  })

  it('resets all active filters from the conditions row', async () => {
    const wrapper = mountBar({ filterValues: { enabled: '1', tags: ['emby'], certificate_id: '7' } })
    await wrapper.find('.resource-list-filter-bar__reset--inline').trigger('click')
    const emitted = wrapper.emitted('update:filter')
    expect(emitted).toContainEqual([{ key: 'enabled', value: '' }])
    expect(emitted).toContainEqual([{ key: 'tags', value: [] }])
    expect(emitted).toContainEqual([{ key: 'certificate_id', value: '' }])
    expect(emitted).toHaveLength(3)
  })

  it('shows the active filter count badge on the trigger', () => {
    const wrapper = mountBar({ filterValues: { enabled: '1', tags: ['emby'] } })
    expect(wrapper.find('.resource-list-filter-bar__filter-badge').text()).toBe('2')
  })

  it('emits search updates and clears the query', async () => {
    const wrapper = mountBar({ q: 'emby' })
    const input = wrapper.find('.resource-list-filter-bar__input')
    await input.setValue('web')
    expect(wrapper.emitted('update:q')).toEqual([['web']])

    await wrapper.find('.resource-list-filter-bar__clear').trigger('click')
    expect(wrapper.emitted('update:q')[1]).toEqual([''])
  })
})
