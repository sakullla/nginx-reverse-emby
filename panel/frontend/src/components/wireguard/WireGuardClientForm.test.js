import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import WireGuardClientForm from './WireGuardClientForm.vue'

describe('WireGuardClientForm', () => {
  it('submits an empty dns list when an edited client DNS field is cleared', async () => {
    const wrapper = mount(WireGuardClientForm, {
      props: {
        initialData: {
          id: 42,
          name: 'phone',
          allowed_ips: ['10.8.0.2/32'],
          dns: ['1.1.1.1'],
          enabled: true
        }
      }
    })

    await wrapper.findAll('textarea').at(1).setValue('')
    await wrapper.find('form').trigger('submit')

    expect(wrapper.emitted('submit')).toEqual([[
      {
        name: 'phone',
        allowed_ips: ['10.8.0.2/32'],
        dns: [],
        enabled: true
      }
    ]])
  })
})
