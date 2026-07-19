import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficQuotaRing from './TrafficQuotaRing.vue'

const ApexChartStub = {
  name: 'apexchart',
  template: '<div data-testid="apexchart" />',
  props: ['type', 'options', 'series', 'height']
}

const mountOptions = {
  global: {
    stubs: {
      apexchart: ApexChartStub
    }
  }
}

describe('TrafficQuotaRing', () => {
  it('uses theme CSS variables for Apex label and stroke colors', () => {
    const wrapper = mount(TrafficQuotaRing, {
      props: {
        usedBytes: 1024,
        quotaBytes: 2048,
        remainingBytes: 1024
      },
      ...mountOptions
    })

    const options = wrapper.vm.chartOptions

    expect(options.theme).toBeUndefined()
    expect(options.tooltip.theme).toBeUndefined()
    expect(options.chart.foreColor).toBe('var(--color-text-secondary)')
    expect(options.plotOptions.pie.donut.labels.value.color).toBe('var(--color-text-primary)')
    expect(options.plotOptions.pie.donut.labels.total.color).toBe('var(--color-text-tertiary)')
    expect(options.stroke.colors).toEqual(['var(--color-bg-surface-raised, var(--color-bg-surface))'])
  })

  it('caps the legend at the top five agents and shows the remainder count', () => {
    const agents = Array.from({ length: 8 }, (_, i) => ({
      agent_id: `node-${i}`,
      name: `node-${i}`,
      used_bytes: (i + 1) * 1024 * 1024
    }))

    const wrapper = mount(TrafficQuotaRing, {
      props: { agents },
      ...mountOptions
    })

    const items = wrapper.findAll('.tqr-legend-item')
    expect(items).toHaveLength(5)
    expect(items[0].text()).toContain('node-7')
    expect(items[4].text()).toContain('node-3')

    const more = wrapper.find('.tqr-legend-more')
    expect(more.exists()).toBe(true)
    expect(more.text()).toContain('+3')
  })

  it('omits the more indicator when agents fit under the cap', () => {
    const wrapper = mount(TrafficQuotaRing, {
      props: {
        agents: [
          { agent_id: 'a', name: 'a', used_bytes: 200 },
          { agent_id: 'b', name: 'b', used_bytes: 100 }
        ]
      },
      ...mountOptions
    })

    expect(wrapper.findAll('.tqr-legend-item')).toHaveLength(2)
    expect(wrapper.find('.tqr-legend-more').exists()).toBe(false)
  })
})
