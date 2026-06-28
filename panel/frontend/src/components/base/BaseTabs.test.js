import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BaseTabs from './BaseTabs.vue'

function mountTabs(props = {}) {
  return mount(BaseTabs, {
    props: {
      tabs: [
        { id: 'rules', label: '规则' },
        { id: 'traffic', label: '流量' },
        { id: 'system', label: '系统信息' },
      ],
      modelValue: 'rules',
      ...props,
    },
  })
}

describe('BaseTabs', () => {
  it('renders a tab button for each tab', () => {
    const wrapper = mountTabs()
    const buttons = wrapper.findAll('.base-tabs__tab')
    expect(buttons).toHaveLength(3)
    expect(buttons[0].text()).toBe('规则')
    expect(buttons[1].text()).toBe('流量')
    expect(buttons[2].text()).toBe('系统信息')
  })

  it('marks the active tab', () => {
    const wrapper = mountTabs({ modelValue: 'traffic' })
    const buttons = wrapper.findAll('.base-tabs__tab')
    expect(buttons[0].classes()).not.toContain('base-tabs__tab--active')
    expect(buttons[1].classes()).toContain('base-tabs__tab--active')
    expect(buttons[2].classes()).not.toContain('base-tabs__tab--active')
    expect(buttons[1].attributes('aria-selected')).toBe('true')
  })

  it('emits update:modelValue when a tab is clicked', async () => {
    const wrapper = mountTabs()
    const buttons = wrapper.findAll('.base-tabs__tab')
    await buttons[2].trigger('click')
    await nextTick()
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)
    expect(wrapper.emitted('update:modelValue')[0]).toEqual(['system'])
  })

  it('does not emit when the active tab is clicked', async () => {
    const wrapper = mountTabs({ modelValue: 'rules' })
    const buttons = wrapper.findAll('.base-tabs__tab')
    await buttons[0].trigger('click')
    await nextTick()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('renders empty when tabs is empty', () => {
    const wrapper = mountTabs({ tabs: [] })
    expect(wrapper.findAll('.base-tabs__tab')).toHaveLength(0)
  })

  it('validates tab shape', () => {
    const spy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mount(BaseTabs, {
      props: { tabs: [{ id: 1, label: 'bad' }], modelValue: '' },
    })
    expect(spy).toHaveBeenCalled()
    spy.mockRestore()
  })
})
