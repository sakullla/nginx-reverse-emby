import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import AgentMetricTile from './AgentMetricTile.vue'

function mountTile(props = {}) {
  return mount(AgentMetricTile, {
    props: {
      icon: 'i-mdi-cpu-64-bit',
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

    expect(wrapper.find('.i-mdi-cpu-64-bit').exists()).toBe(true)
    expect(wrapper.text()).toContain('CPU')
    expect(wrapper.text()).toContain('1.0 / 8 核')
  })

  it('uses BaseMetricBar for regular metrics', () => {
    const wrapper = mountTile()

    expect(wrapper.find('.agent-metric-tile__label').text()).toBe('CPU')

    const bar = wrapper.find('[data-testid="agent-metric-tile-metric-bar"]')
    expect(bar.exists()).toBe(true)
    expect(bar.find('.base-metric-bar__label').text()).toBe('')
    expect(bar.find('.base-metric-bar__value').text()).toContain('1.0 / 8 核')

    const fill = bar.find('.base-metric-bar__fill')
    expect(fill.attributes('style')).toContain('width: 12.4%')
    expect(fill.classes()).toContain('base-metric-bar__fill--success')
  })

  it('clamps percent to [0, 100] via BaseMetricBar', () => {
    const wrapper = mountTile({ percent: 150 })

    const bar = wrapper.find('[data-testid="agent-metric-tile-metric-bar"]')
    expect(bar.find('.base-metric-bar__fill').attributes('style')).toContain('width: 100%')
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

  it('lays out down/up rate on a single line without breaking the value', () => {
    const wrapper = mountTile({
      icon: 'i-mdi-network',
      label: '网络',
      value: null,
      percent: null,
      tone: 'neutral',
      networkDown: '12.34 MiB/s',
      networkUp: '1.00 KiB/s',
    })

    const network = wrapper.find('.agent-metric-tile__network')
    expect(network.exists()).toBe(true)

    // Down and up must share one horizontal line (row), not stack vertically,
    // and a rate string must not break between number and unit. Assert against
    // the component source because scoped styles are not reflected on inline
    // element.style in jsdom.
    const source = readFileSync(resolve(process.cwd(), 'src/components/AgentMetricTile.vue'), 'utf8')
    const networkRule = source.slice(source.indexOf('.agent-metric-tile__network {'))
    expect(networkRule).toContain('flex-direction: row')
    const valueRule = source.slice(source.indexOf('.agent-metric-tile__network-value {'))
    expect(valueRule).toContain('white-space: nowrap')
    expect(valueRule).not.toContain('overflow-wrap: anywhere')
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
