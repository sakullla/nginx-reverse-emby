import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import CreateAgentPicker from './CreateAgentPicker.vue'

const now = Date.now()
const agents = [
  { id: 'local', name: '本机', status: 'online', last_seen_at: new Date(now - 30_000).toISOString() },
  { id: 'edge-1', name: 'Edge One', status: 'online', last_seen_at: new Date(now - 120_000).toISOString() },
  { id: 'edge-2', name: 'Edge Two', status: 'offline', last_seen_at: new Date(now - 86_400_000).toISOString() }
]

function mountPicker(props = {}) {
  return mount(CreateAgentPicker, {
    props: { visible: true, agents, ...props },
    attachTo: document.body
  })
}

describe('CreateAgentPicker', () => {
  it('renders all agents and emits selected id when clicked', async () => {
    const wrapper = mountPicker()
    expect(wrapper.text()).toContain('本机')
    expect(wrapper.text()).toContain('Edge One')

    await wrapper.findAll('.create-agent-picker__item')
      .find((node) => node.text().includes('Edge One'))
      .trigger('click')

    expect(wrapper.emitted('select')?.[0]).toEqual([agents[1]])
  })

  it('does not display agent ids in the list', () => {
    const wrapper = mountPicker()
    expect(wrapper.text()).toContain('Edge One')
    expect(wrapper.text()).not.toContain('edge-1')
    expect(wrapper.find('.create-agent-picker__id').exists()).toBe(false)
  })

  it('filters agents by name only and shows empty state when none match', async () => {
    const wrapper = mountPicker()
    await wrapper.find('.create-agent-picker__search-input').setValue('edge-2')
    expect(wrapper.text()).not.toContain('Edge Two')
    expect(wrapper.find('.create-agent-picker__empty').exists()).toBe(true)

    await wrapper.find('.create-agent-picker__search-input').setValue('Edge Two')
    expect(wrapper.text()).toContain('Edge Two')
  })

  it('shows last seen time in items instead of id', () => {
    const wrapper = mountPicker()
    expect(wrapper.find('.create-agent-picker__time').exists()).toBe(true)
    expect(wrapper.text()).toContain('刚刚')
  })

  it('filters by online/offline status', async () => {
    const wrapper = mountPicker()

    await wrapper.find('.create-agent-picker__filter-btn--offline').trigger('click')
    expect(wrapper.text()).toContain('Edge Two')
    expect(wrapper.text()).not.toContain('Edge One')
    expect(wrapper.text()).not.toContain('本机')

    await wrapper.find('.create-agent-picker__filter-btn--online').trigger('click')
    expect(wrapper.text()).toContain('Edge One')
    expect(wrapper.text()).toContain('本机')
    expect(wrapper.text()).not.toContain('Edge Two')
  })

  it('sorts by name when sort-by-name is selected', async () => {
    const wrapper = mountPicker()
    await wrapper.find('.create-agent-picker__sort-btn--name').trigger('click')
    const names = wrapper.findAll('.create-agent-picker__name')
      .map((node) => node.text())
    expect(names).toEqual(['本机', 'Edge One', 'Edge Two'])
  })

  it('emits cancel when clicking cancel, close, or overlay', async () => {
    const wrapper = mountPicker()
    await wrapper.find('.create-agent-picker__close').trigger('click')
    expect(wrapper.emitted('cancel')).toBeTruthy()

    const overlay = mountPicker()
    await overlay.find('.create-agent-picker-overlay').trigger('click')
    expect(overlay.emitted('cancel')).toBeTruthy()
  })

  it('does not render when not visible', () => {
    const wrapper = mountPicker({ visible: false })
    expect(wrapper.find('.create-agent-picker-overlay').exists()).toBe(false)
  })
})
