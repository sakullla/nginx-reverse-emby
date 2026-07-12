import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import L4RuleItem from './L4RuleItem.vue'

function mountItem(overrides = {}) {
  return mount(L4RuleItem, {
    props: {
      rule: {
        id: 1,
        protocol: 'tcp',
        listen_mode: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 9000,
        backends: [{ host: '127.0.0.1', port: 9001 }],
        load_balancing: { strategy: 'adaptive' },
        enabled: true,
        tags: [],
        ...overrides
      }
    },
    attachTo: document.body,
  })
}

describe('L4RuleItem', () => {
  it('omits redundant default TCP and UDP forwarding badges', () => {
    const tcp = mountItem({ protocol: 'tcp', listen_mode: 'tcp' })
    const udp = mountItem({ protocol: 'udp', listen_mode: 'udp' })

    expect(tcp.text()).not.toContain('TCP转发')
    expect(udp.text()).not.toContain('UDP转发')
  })

  it('keeps non-default listen mode badges', () => {
    const wrapper = mountItem({ listen_mode: 'proxy' })

    expect(wrapper.text()).toContain('代理')
  })
})

describe('L4RuleItem agent ownership badge', () => {
  it('renders item agent_name without page selected agent', () => {
    const wrapper = mountItem({ agent_name: 'l4-node', agent_id: 'l4a' })
    expect(wrapper.text()).toContain('l4-node')
  })
})

describe('L4RuleItem redesign', () => {
  it('uses listen host:port as title', () => {
    const wrapper = mountItem()
    expect(wrapper.find('.base-list-card__title').text()).toBe('0.0.0.0:9000')
  })

  it('labels active status as 生效中 not 启用', () => {
    const wrapper = mountItem({ enabled: true })
    expect(wrapper.text()).toContain('生效中')
    expect(wrapper.text()).not.toMatch(/(^|[^已])启用/)
  })

  async function openMenu(wrapper) {
    // Close any other open teleported menus first.
    document.body
      .querySelectorAll('[data-testid="base-action-menu-panel"]')
      .forEach((el) => {
        el.style.display = 'none'
        el.setAttribute('aria-hidden', 'true')
      })
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await nextTick()
    await nextTick()
  }

  function openPanel() {
    return document.body.querySelector(
      '[data-testid="base-action-menu-panel"]:not([aria-hidden="true"])',
    )
  }

  function bodyMenuItem(id) {
    return openPanel()?.querySelector(`[data-testid="base-action-menu-item-${id}"]`) || null
  }

  it('exposes toggle and edit; secondary actions in menu', async () => {
    const wrapper = mountItem({ protocol: 'tcp' })
    expect(wrapper.find('button[title="停用"]').exists()).toBe(true)
    expect(wrapper.find('button[title="编辑"]').exists()).toBe(true)
    expect(wrapper.find('button[title="诊断"]').exists()).toBe(false)

    await openMenu(wrapper)
    expect(bodyMenuItem('diagnose')).toBeTruthy()
    expect(bodyMenuItem('copy')).toBeTruthy()
    expect(bodyMenuItem('delete')).toBeTruthy()
  })

  it('omits diagnose for non-TCP protocols', async () => {
    const wrapper = mountItem({ protocol: 'udp' })
    await openMenu(wrapper)
    const panel = openPanel()
    expect(panel).toBeTruthy()
    expect(panel.querySelector('[data-testid="base-action-menu-item-diagnose"]')).toBeNull()
    expect(panel.querySelector('[data-testid="base-action-menu-item-delete"]')).toBeTruthy()
  })
})
