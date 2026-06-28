import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseMetricBar from './BaseMetricBar.vue'

function mountBar(props = {}) {
  return mount(BaseMetricBar, {
    props: {
      label: 'CPU',
      value: '12.4%',
      percent: 12.4,
      tone: 'success',
      ...props,
    },
  })
}

describe('BaseMetricBar', () => {
  it('renders label and value', () => {
    const wrapper = mountBar()
    expect(wrapper.text()).toContain('CPU')
    expect(wrapper.text()).toContain('12.4%')
  })

  it('renders value with unit', () => {
    const wrapper = mountBar({ value: 10, unit: 'GiB', percent: 50 })
    expect(wrapper.text()).toContain('10 GiB')
  })

  it('sets fill width to percent', () => {
    const wrapper = mountBar({ percent: 45 })
    const fill = wrapper.find('.base-metric-bar__fill')
    expect(fill.attributes('style')).toContain('width: 45%')
  })

  it('clamps percent above 100 to 100', () => {
    const wrapper = mountBar({ percent: 150 })
    const fill = wrapper.find('.base-metric-bar__fill')
    expect(fill.attributes('style')).toContain('width: 100%')
  })

  it('clamps percent below 0 to 0', () => {
    const wrapper = mountBar({ percent: -10 })
    const fill = wrapper.find('.base-metric-bar__fill')
    expect(fill.attributes('style')).toContain('width: 0%')
  })

  it('uses tone class on fill', () => {
    const wrapper = mountBar({ tone: 'danger' })
    const fill = wrapper.find('.base-metric-bar__fill')
    expect(fill.classes()).toContain('base-metric-bar__fill--danger')
  })

  it('falls back to neutral for invalid tone', () => {
    const wrapper = mountBar({ tone: 'invalid' })
    const fill = wrapper.find('.base-metric-bar__fill')
    expect(fill.classes()).toContain('base-metric-bar__fill--neutral')
  })

  it('renders with null value', () => {
    const wrapper = mountBar({ value: null })
    expect(wrapper.find('.base-metric-bar__value').exists()).toBe(false)
    expect(wrapper.text()).toContain('CPU')
  })
})
