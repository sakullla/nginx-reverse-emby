import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AgentMetricTile from './AgentMetricTile.vue'

function mountTile(props = {}) {
  return mount(AgentMetricTile, {
    props: {
      icon: 'i-mdi-cpu',
      label: 'CPU',
      value: '1.0 / 8 核',
      percent: 12.4,
      tone: 'success',
      ...props,
    },
  })
}

describe('AgentMetricTile', () => {
  it('renders icon, label and value', () => {
    const wrapper = mountTile()

    expect(wrapper.find('.i-mdi-cpu').exists()).toBe(true)
    expect(wrapper.text()).toContain('CPU')
    expect(wrapper.text()).toContain('1.0 / 8 核')
  })

  it('uses BaseMetricBar for regular metrics', () => {
    const wrapper = mountTile()

    const bar = wrapper.find('[data-testid="agent-metric-tile-metric-bar"]')
    expect(bar.exists()).toBe(true)
    expect(bar.find('.base-metric-bar__label').text()).toBe('CPU')
    expect(bar.find('.base-metric-bar__value').text()).toContain('1.0 / 8 核')

    const fill = bar.find('.base-metric-bar__fill')
    expect(fill.attributes('style')).toContain('width: 12.4%')
    expect(fill.classes()).toContain('base-metric-bar__fill--success')
  })

  it('renders network down/up rows when network props are provided', () => {
    const wrapper = mountTile({
      icon: 'i-mdi-network',
      label: '网络',
      value: null,
      percent: null,
      tone: 'neutral',
      networkDown: '2.00 KiB/s',
      networkUp: '1.00 KiB/s',
    })

    expect(wrapper.find('[data-testid="agent-metric-tile-metric-bar"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="agent-metric-tile-network-down"]').text()).toContain('2.00 KiB/s')
    expect(wrapper.find('[data-testid="agent-metric-tile-network-up"]').text()).toContain('1.00 KiB/s')
  })

  it('shows placeholder for missing values', () => {
    const wrapper = mountTile({ value: null, percent: null })

    expect(wrapper.find('.base-metric-bar__value').text()).toContain('—')
    expect(wrapper.text()).toContain('CPU')
  })

  it('shows placeholder for missing network values', () => {
    const wrapper = mountTile({
      icon: 'i-mdi-network',
      label: '网络',
      value: null,
      percent: null,
      networkDown: null,
      networkUp: '1.00 KiB/s',
    })

    expect(wrapper.find('[data-testid="agent-metric-tile-network-down"]').text()).toContain('—')
    expect(wrapper.find('[data-testid="agent-metric-tile-network-up"]').text()).toContain('1.00 KiB/s')
  })

  it('applies compact variant', () => {
    const wrapper = mountTile({ variant: 'compact' })
    expect(wrapper.find('[data-variant="compact"]').exists()).toBe(true)
  })
})
