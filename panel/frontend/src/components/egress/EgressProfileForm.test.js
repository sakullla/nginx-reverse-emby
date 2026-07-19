import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import EgressProfileForm from './EgressProfileForm.vue'

function mountForm(initialData = null) {
  return mount(EgressProfileForm, {
    props: { initialData }
  })
}

function inputByName(wrapper, name) {
  return wrapper.get(`[name="${name}"]`)
}

describe('EgressProfileForm', () => {
  it('submits a socks profile payload', async () => {
    const wrapper = mountForm()

    await inputByName(wrapper, 'name').setValue('office socks')
    await inputByName(wrapper, 'type').setValue('socks')
    await inputByName(wrapper, 'proxy_url').setValue('socks5://127.0.0.1:1080')
    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('submit')[0][0]).toMatchObject({
      name: 'office socks',
      type: 'socks',
      proxy_url: 'socks5://127.0.0.1:1080',
      enabled: true
    })
  })

  it('allows saving an unchanged redacted proxy url', async () => {
    const wrapper = mountForm({
      id: 17,
      name: 'office socks',
      type: 'socks',
      proxy_url: 'socks5://user:xxxxx@127.0.0.1:1080',
      enabled: true
    })

    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('submit')[0][0]).toMatchObject({
      name: 'office socks',
      type: 'socks',
      proxy_url: 'socks5://user:xxxxx@127.0.0.1:1080',
      enabled: true
    })
  })

  it('submits a wireguard profile payload', async () => {
    const wrapper = mountForm()

    await inputByName(wrapper, 'name').setValue('wg exit')
    await inputByName(wrapper, 'type').setValue('wireguard')
    await inputByName(wrapper, 'private_key').setValue('private-key')
    await inputByName(wrapper, 'addresses').setValue('10.42.0.2/32\nfd42::2/128')
    await inputByName(wrapper, 'peer_public_key').setValue('peer-public')
    await inputByName(wrapper, 'peer_endpoint').setValue('127.0.0.1:51820')
    await inputByName(wrapper, 'peer_allowed_ips').setValue('0.0.0.0/0\n::/0')
    await inputByName(wrapper, 'dns').setValue('1.1.1.1')
    await inputByName(wrapper, 'mtu').setValue('1420')
    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('submit')[0][0]).toMatchObject({
      name: 'wg exit',
      type: 'wireguard',
      proxy_url: '',
      wireguard_config: {
        private_key: 'private-key',
        addresses: ['10.42.0.2/32', 'fd42::2/128'],
        dns: ['1.1.1.1'],
        mtu: 1420,
        peers: [{
          public_key: 'peer-public',
          endpoint: '127.0.0.1:51820',
          allowed_ips: ['0.0.0.0/0', '::/0']
        }]
      }
    })
  })

  it('preserves existing WireGuard peer fields when editing', async () => {
    const wrapper = mountForm({
      id: 42,
      name: 'wg exit',
      type: 'wireguard',
      enabled: true,
      wireguard_config: {
        private_key: 'xxxxx',
        addresses: ['10.42.0.2/32'],
        peers: [{
          public_key: 'peer-public',
          endpoint: '127.0.0.1:51820',
          allowed_ips: ['0.0.0.0/0'],
          preshared_key: 'xxxxx',
          reserved: [1, 2, 3],
          persistent_keepalive_seconds: 25
        }]
      }
    })

    await inputByName(wrapper, 'name').setValue('wg exit renamed')
    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('submit')[0][0]).toMatchObject({
      name: 'wg exit renamed',
      type: 'wireguard',
      wireguard_config: {
        peers: [{
          public_key: 'peer-public',
          endpoint: '127.0.0.1:51820',
          allowed_ips: ['0.0.0.0/0'],
          preshared_key: 'xxxxx',
          reserved: [1, 2, 3],
          persistent_keepalive_seconds: 25
        }]
      }
    })
  })

  it('preserves additional WireGuard peers when editing', async () => {
    const wrapper = mountForm({
      id: 43,
      name: 'wg multi',
      type: 'wireguard',
      enabled: true,
      wireguard_config: {
        private_key: 'xxxxx',
        addresses: ['10.42.0.2/32'],
        peers: [
          {
            public_key: 'peer-a',
            endpoint: 'a.example.com:51820',
            allowed_ips: ['10.42.0.0/24'],
            preshared_key: 'xxxxx'
          },
          {
            public_key: 'peer-b',
            endpoint: 'b.example.com:51820',
            allowed_ips: ['10.43.0.0/24'],
            persistent_keepalive_seconds: 30
          }
        ]
      }
    })

    await inputByName(wrapper, 'description').setValue('rename only')
    await wrapper.get('form').trigger('submit')

    const payload = wrapper.emitted('submit')[0][0]
    expect(payload.wireguard_config.peers).toHaveLength(2)
    expect(payload.wireguard_config.peers[1]).toMatchObject({
      public_key: 'peer-b',
      endpoint: 'b.example.com:51820',
      allowed_ips: ['10.43.0.0/24'],
      persistent_keepalive_seconds: 30
    })
  })
})
