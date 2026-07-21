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

  it('offers only direct and proxy profile types', () => {
    const wrapper = mountForm()

    expect(inputByName(wrapper, 'type').findAll('option').map((option) => option.element.value)).toEqual([
      'direct',
      'socks',
      'http'
    ])
  })

  it('switches from a proxy profile to direct without submitting its url', async () => {
    const wrapper = mountForm({
      id: 42,
      name: 'office proxy',
      type: 'http',
      proxy_url: 'http://127.0.0.1:8080',
      enabled: true
    })

    await inputByName(wrapper, 'type').setValue('direct')
    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('submit')[0][0]).toMatchObject({
      name: 'office proxy',
      type: 'direct',
      proxy_url: ''
    })
  })
})
