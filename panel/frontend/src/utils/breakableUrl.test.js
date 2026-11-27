import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { breakableUrl } from './breakableUrl.js'

describe('breakableUrl', () => {
  it('ends segments after . / and :', () => {
    expect(breakableUrl('https://media.example.com/emby')).toEqual([
      'https:', '/', '/', 'media.', 'example.', 'com/', 'emby'
    ])
    expect(breakableUrl('10.0.1.12:8096')).toEqual(['10.', '0.', '1.', '12:', '8096'])
  })

  it('round-trips the original text and handles empty values', () => {
    const value = 'very-long-subdomain.staging.services.internal.company.io'
    expect(breakableUrl(value).join('')).toBe(value)
    expect(breakableUrl('')).toEqual([])
    expect(breakableUrl(null)).toEqual([])
  })

  it('renders as escaped text with <wbr> break opportunities', () => {
    const Probe = defineComponent({
      props: { value: String },
      setup(props) {
        return () => {
          const parts = breakableUrl(props.value)
          return h('span', parts.flatMap((part, i) => (i < parts.length - 1 ? [part, h('wbr')] : [part])))
        }
      }
    })
    const wrapper = mount(Probe, { props: { value: '<b>a.b</b>' } })
    expect(wrapper.find('b').exists()).toBe(false)
    expect(wrapper.findAll('wbr').length).toBeGreaterThan(0)
    expect(wrapper.text()).toBe('<b>a.b</b>')
  })
})
