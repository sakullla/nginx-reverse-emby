import { describe, expect, it, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BaseActionMenu from './BaseActionMenu.vue'

const defaultItems = [
  { id: 'copy', label: '复制' },
  { id: 'delete', label: '删除', tone: 'danger' },
  { id: 'diagnose', label: '诊断', disabled: true },
]

function mountMenu(props = {}) {
  return mount(BaseActionMenu, {
    props: {
      items: defaultItems,
      ...props,
    },
    attachTo: document.body,
  })
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('BaseActionMenu', () => {
  it('does not render when items is empty', () => {
    const wrapper = mountMenu({ items: [] })
    expect(wrapper.find('.base-action-menu').exists()).toBe(false)
    expect(wrapper.find('.base-action-menu__trigger').exists()).toBe(false)
  })

  it('renders ⋯ trigger when items exist', () => {
    const wrapper = mountMenu()
    expect(wrapper.find('.base-action-menu').exists()).toBe(true)
    const trigger = wrapper.find('.base-action-menu__trigger')
    expect(trigger.exists()).toBe(true)
    expect(trigger.attributes('title')).toBe('更多操作')
  })

  it('opens panel on trigger click and lists items', async () => {
    const wrapper = mountMenu()
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(false)
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    const panel = wrapper.find('[data-testid="base-action-menu-panel"]')
    expect(panel.exists()).toBe(true)
    expect(wrapper.find('[data-testid="base-action-menu-item-copy"]').text()).toBe('复制')
    expect(wrapper.find('[data-testid="base-action-menu-item-delete"]').text()).toBe('删除')
  })

  it('emits select and closes when an item is chosen', async () => {
    const wrapper = mountMenu()
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="base-action-menu-item-delete"]').trigger('click')
    await nextTick()
    expect(wrapper.emitted('select')).toHaveLength(1)
    expect(wrapper.emitted('select')[0][0]).toMatchObject({ id: 'delete', label: '删除', tone: 'danger' })
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(false)
  })

  it('does not emit select for disabled items', async () => {
    const wrapper = mountMenu()
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    const item = wrapper.find('[data-testid="base-action-menu-item-diagnose"]')
    expect(item.attributes('disabled')).toBeDefined()
    await item.trigger('click')
    await nextTick()
    expect(wrapper.emitted('select')).toBeUndefined()
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(true)
  })

  it('closes on Escape', async () => {
    const wrapper = mountMenu()
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(true)
    await wrapper.find('.base-action-menu').trigger('keydown.escape')
    await nextTick()
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(false)
  })

  it('closes on outside pointer down', async () => {
    const wrapper = mountMenu()
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(true)

    document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(false)
  })

  it('toggles closed when trigger is clicked again', async () => {
    const wrapper = mountMenu()
    const trigger = wrapper.find('.base-action-menu__trigger')
    await trigger.trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(true)
    await trigger.trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="base-action-menu-panel"]').exists()).toBe(false)
  })
})
