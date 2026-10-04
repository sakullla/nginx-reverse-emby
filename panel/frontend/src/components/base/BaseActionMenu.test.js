import { describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import BaseActionMenu from './BaseActionMenu.vue'

const items = [
  { id: 'edit', label: '编辑' },
  { id: 'copy', label: '复制', disabled: true },
  { id: 'delete', label: '删除', tone: 'danger' }
]

function panelItems() {
  return Array.from(document.querySelectorAll('.base-action-menu__item'))
}

// openMenu awaits two nextTicks (position + paint passes) before focusing
async function openMenu(wrapper) {
  await wrapper.get('.base-action-menu__trigger').trigger('click')
  await nextTick()
  await nextTick()
}

async function keydownOn(el, key) {
  el.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
  await nextTick()
}

describe('BaseActionMenu keyboard support', () => {
  it('opens with the trigger, focuses the first enabled item and supports arrow keys', async () => {
    const wrapper = mount(BaseActionMenu, { props: { items }, attachTo: document.body })
    const trigger = wrapper.get('.base-action-menu__trigger')
    expect(trigger.attributes('aria-haspopup')).toBe('menu')
    expect(trigger.attributes('aria-expanded')).toBe('false')

    await openMenu(wrapper)
    expect(trigger.attributes('aria-expanded')).toBe('true')
    const menuItems = panelItems()
    expect(menuItems).toHaveLength(3)
    expect(document.activeElement).toBe(menuItems[0])

    await keydownOn(menuItems[0], 'ArrowDown')
    expect(document.activeElement).toBe(menuItems[2])

    await keydownOn(menuItems[2], 'ArrowDown')
    expect(document.activeElement).toBe(menuItems[0])

    await keydownOn(menuItems[0], 'End')
    expect(document.activeElement).toBe(menuItems[2])

    wrapper.unmount()
  })

  it('Escape closes the menu and returns focus to the trigger', async () => {
    const wrapper = mount(BaseActionMenu, { props: { items }, attachTo: document.body })
    const trigger = wrapper.get('.base-action-menu__trigger')
    await openMenu(wrapper)
    expect(document.activeElement).toBe(panelItems()[0])

    await keydownOn(panelItems()[0], 'Escape')
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(trigger.element)

    wrapper.unmount()
  })
})
