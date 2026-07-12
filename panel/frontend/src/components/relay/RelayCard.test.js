import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import RelayCard from './RelayCard.vue'

function mountRelayCard(listener) {
  return mount(RelayCard, {
    props: {
      listener: {
        id: 5,
        name: 'relay-wg-local',
        enabled: true,
        bind_hosts: ['10.8.0.1'],
        listen_port: 19001,
        public_host: 'wg-relay.example.com',
        public_port: 51820,
        certificate_source: 'auto_relay_ca',
        trust_mode_source: 'auto',
        allow_self_signed: true,
        tags: [],
        ...listener
      }
    }
  })
}

describe('RelayCard transport display', () => {
  it('labels WireGuard relay listeners as WireGuard instead of TLS/TCP', () => {
    const wrapper = mountRelayCard({ transport_mode: 'wireguard' })

    expect(wrapper.text()).toContain('WireGuard')
    expect(wrapper.text()).toContain('Profile Endpoint')
    expect(wrapper.text()).toContain('Relay 内部监听')
    expect(wrapper.text()).not.toContain('TLS/TCP')
  })
})

describe('RelayCard agent ownership badge', () => {
  it('renders agent_name from listener without page agent', () => {
    const wrapper = mountRelayCard({ agent_name: 'relay-node', agent_id: 'r1' })
    expect(wrapper.text()).toContain('relay-node')
  })
})

describe('RelayCard redesign', () => {
  it('uses listener name as title and 生效中 status', () => {
    const wrapper = mountRelayCard({ name: 'edge-relay', enabled: true })
    expect(wrapper.find('.base-list-card__title').text()).toBe('edge-relay')
    expect(wrapper.text()).toContain('生效中')
  })

  it('shows 已禁用 when disabled', () => {
    const wrapper = mountRelayCard({ enabled: false })
    expect(wrapper.text()).toContain('已禁用')
  })

  it('exposes toggle/edit and puts delete in more menu', async () => {
    document.body
      .querySelectorAll('[data-testid="base-action-menu-panel"]')
      .forEach((el) => {
        el.style.display = 'none'
        el.setAttribute('aria-hidden', 'true')
      })
    const wrapper = mount(RelayCard, {
      props: {
        listener: {
          id: 5,
          name: 'relay-wg-local',
          enabled: true,
          bind_hosts: ['10.8.0.1'],
          listen_port: 19001,
          tags: [],
        },
      },
      attachTo: document.body,
    })
    expect(wrapper.find('button[title="停用"]').exists()).toBe(true)
    expect(wrapper.find('button[title="编辑"]').exists()).toBe(true)
    expect(wrapper.find('button[title="删除"]').exists()).toBe(false)
    await wrapper.find('.base-action-menu__trigger').trigger('click')
    await Promise.resolve()
    await Promise.resolve()
    const panel = document.body.querySelector(
      '[data-testid="base-action-menu-panel"]:not([aria-hidden="true"])',
    )
    const item = panel?.querySelector('[data-testid="base-action-menu-item-delete"]')
    expect(item).toBeTruthy()
    item.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await Promise.resolve()
    await Promise.resolve()
    expect(wrapper.emitted('delete')).toBeTruthy()
  })
})
