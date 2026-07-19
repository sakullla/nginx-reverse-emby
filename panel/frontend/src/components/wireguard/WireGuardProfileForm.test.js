import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import WireGuardProfileForm from './WireGuardProfileForm.vue'

function mountForm(initialData = null) {
  return mount(WireGuardProfileForm, {
    props: { initialData }
  })
}

function textareaByLabel(wrapper, labelText) {
  const group = wrapper
    .findAll('.form-group')
    .find((item) => item.find('.form-label').exists() && item.find('.form-label').text() === labelText)
  if (!group) throw new Error(`Missing form group: ${labelText}`)
  return group.get('textarea')
}

describe('WireGuardProfileForm', () => {
  it('submits edit defaults when bind and interface address fields are blank', async () => {
    const wrapper = mountForm({
      id: 7,
      name: 'edge-wg',
      private_key: 'xxxxx',
      listen_port: 51820,
      addresses: ['192.168.0.109'],
      interface_addresses: ['10.8.0.1/24', 'fd10:8::1/64'],
      peers: [],
      dns: [],
      mtu: 1280,
      enabled: true,
      tags: []
    })

    await textareaByLabel(wrapper, 'Addresses（每行一个）').setValue('')
    await textareaByLabel(wrapper, 'WG 分配地址（每行一个）').setValue('')
    await wrapper.get('form').trigger('submit')

    const payload = wrapper.emitted('submit')[0][0]
    expect(payload.addresses).toEqual(['0.0.0.0'])
    expect(payload.interface_addresses).toEqual(['10.8.0.1/24', 'fd10:8::1/64'])
  })
})
