import { describe, expect, it, afterEach } from 'vitest'
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

function clearTeleportedPanels() {
  document.body.querySelectorAll('[data-testid="base-action-menu-panel"]').forEach((el) => el.remove())
}

afterEach(() => {
  clearTeleportedPanels()
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

  async function openPanel(wrapper) {
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    await nextTick()
  }

  function bodyPanel() {
    return document.body.querySelector('[data-testid="base-action-menu-panel"]')
  }

  function isPanelOpen(wrapper) {
    return wrapper.find('.base-action-menu__trigger').attributes('aria-expanded') === 'true'
  }

  function isPanelVisible(panel = bodyPanel()) {
    if (!panel) return false
    return panel.style.display !== 'none' && panel.getAttribute('aria-hidden') !== 'true'
  }

  it('opens panel on trigger click and lists items (teleported to body)', async () => {
    const wrapper = mountMenu()
    // v-show keeps the node mounted; starts hidden
    expect(isPanelVisible()).toBe(false)
    await openPanel(wrapper)
    const panel = bodyPanel()
    expect(isPanelVisible(panel)).toBe(true)
    expect(panel.querySelector('[data-testid="base-action-menu-item-copy"]')?.textContent).toBe('复制')
    expect(panel.querySelector('[data-testid="base-action-menu-item-delete"]')?.textContent).toBe('删除')
    // fixed positioning so overflow:hidden ancestors cannot clip it
    expect(panel.style.position).toBe('fixed')
  })

  it('emits select and closes when an item is chosen', async () => {
    const wrapper = mountMenu()
    await openPanel(wrapper)
    bodyPanel().querySelector('[data-testid="base-action-menu-item-delete"]').click()
    await nextTick()
    expect(wrapper.emitted('select')).toHaveLength(1)
    expect(wrapper.emitted('select')[0][0]).toMatchObject({ id: 'delete', label: '删除', tone: 'danger' })
    expect(isPanelOpen(wrapper)).toBe(false)
    expect(isPanelVisible()).toBe(false)
  })

  it('does not emit select for disabled items', async () => {
    const wrapper = mountMenu()
    await openPanel(wrapper)
    const item = bodyPanel().querySelector('[data-testid="base-action-menu-item-diagnose"]')
    expect(item.disabled).toBe(true)
    item.click()
    await nextTick()
    expect(wrapper.emitted('select')).toBeUndefined()
    expect(isPanelOpen(wrapper)).toBe(true)
  })

  it('closes on Escape', async () => {
    const wrapper = mountMenu()
    await openPanel(wrapper)
    expect(isPanelOpen(wrapper)).toBe(true)
    await wrapper.find('.base-action-menu').trigger('keydown.escape')
    await nextTick()
    expect(isPanelOpen(wrapper)).toBe(false)
    expect(isPanelVisible()).toBe(false)
  })

  it('closes on outside pointer down', async () => {
    const wrapper = mountMenu()
    await openPanel(wrapper)
    expect(isPanelOpen(wrapper)).toBe(true)

    // Click a true outside node (not the teleported panel under body).
    const outside = document.createElement('div')
    document.body.appendChild(outside)
    outside.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()
    expect(isPanelOpen(wrapper)).toBe(false)
    expect(isPanelVisible()).toBe(false)
    outside.remove()
  })

  it('toggles closed when trigger is clicked again', async () => {
    const wrapper = mountMenu()
    const trigger = wrapper.find('.base-action-menu__trigger')
    await openPanel(wrapper)
    expect(isPanelOpen(wrapper)).toBe(true)
    await trigger.trigger('click')
    await nextTick()
    expect(isPanelOpen(wrapper)).toBe(false)
    expect(isPanelVisible()).toBe(false)
  })
})
