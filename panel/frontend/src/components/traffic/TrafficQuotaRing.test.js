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
})
