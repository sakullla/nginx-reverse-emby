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
