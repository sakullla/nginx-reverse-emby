import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import BottomNav from './BottomNav.vue'

const route = reactive({ path: '/', fullPath: '/', name: 'dashboard' })
const pluginRoutes = ref([])

vi.mock('vue-router', () => ({
  useRoute: () => route,
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' }
}))
vi.mock('../../hooks/usePluginUIRoutes', () => ({
  usePluginUIRoutes: () => ({ routes: pluginRoutes })
}))
vi.mock('../../context/useAccessControl', () => ({
  useAccessControl: () => ({ refreshActor: async () => undefined })
}))

let wrapper
afterEach(() => {
  wrapper?.unmount()
  route.path = '/'
  route.fullPath = '/'
  pluginRoutes.value = []
})

function render() {
  wrapper = mount(BottomNav, { attachTo: document.body })
  return wrapper.get('button[aria-label="更多"]')
}

describe('mobile navigation', () => {
  it('offers direct access to nodes without opening More', () => {
    render()
    expect(wrapper.get('a[href="/agents"]').text()).toBe('节点')
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
  })

  it('moves through menu items and restores trigger focus with Escape', async () => {
    const trigger = render()
    trigger.element.focus()
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await flushPromises()
    const items = wrapper.findAll('[role="menuitem"]')
    expect(document.activeElement).toBe(items[0].element)
    await items[0].trigger('keydown', { key: 'End' })
    expect(document.activeElement).toBe(items.at(-1).element)
    await items.at(-1).trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(items[0].element)
    await items[0].trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    expect(document.activeElement).toBe(trigger.element)
    expect(trigger.attributes('aria-expanded')).toBe('false')
  })

  it('closes on keyboard focus leaving the menu and on navigation', async () => {
    const trigger = render()
    await trigger.trigger('click')
    await flushPromises()
    wrapper.get('a[href="/agents"]').element.focus()
    await flushPromises()
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    await trigger.trigger('click')
    route.path = '/rules'
    route.fullPath = '/rules'
    await flushPromises()
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
  })

  it('includes plugin links in keyboard navigation', async () => {
    pluginRoutes.value = [{ id: 'plugin-preview', label: '插件页面', href: '/plugin-ui/preview' }]
    const trigger = render()
    await trigger.trigger('click')
    await flushPromises()
    const link = wrapper.get('a[href="/plugin-ui/preview"]')
    expect(link.attributes('role')).toBe('menuitem')
    await link.trigger('click')
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
  })
})

describe('mobile navigation state semantics', () => {
  it('marks the active main item with aria-current', async () => {
    render()
    route.path = '/rules'
    route.fullPath = '/rules'
    await flushPromises()
    expect(wrapper.get('a[href="/rules"]').attributes('aria-current')).toBe('page')
    expect(wrapper.get('a[href="/"]').attributes('aria-current')).toBeUndefined()
    expect(wrapper.get('a[href="/certs"]').attributes('aria-current')).toBeUndefined()
    expect(wrapper.get('a[href="/agents"]').attributes('aria-current')).toBeUndefined()
  })

  it('keeps /pki active on the certificate item', async () => {
    render()
    route.path = '/pki'
    route.fullPath = '/pki'
    await flushPromises()
    expect(wrapper.get('a[href="/certs"]').attributes('aria-current')).toBe('page')
  })

  it('offers version policy in the more menu with current state', async () => {
    const trigger = render()
    await trigger.trigger('click')
    await flushPromises()
    const versionLink = wrapper.get('a[href="/versions"]')
    expect(versionLink.text()).toContain('版本策略')
    expect(versionLink.attributes('aria-current')).toBeUndefined()
    route.path = '/versions'
    route.fullPath = '/versions'
    await flushPromises()
    // the menu closes on route change; reopen to inspect the updated state
    await trigger.trigger('click')
    await flushPromises()
    expect(wrapper.get('a[href="/versions"]').attributes('aria-current')).toBe('page')
    expect(trigger.attributes('class')).toContain('active')
  })
})
