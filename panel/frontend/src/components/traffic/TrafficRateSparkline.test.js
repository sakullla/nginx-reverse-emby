import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficRateSparkline from './TrafficRateSparkline.vue'

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

describe('TrafficRateSparkline', () => {
  it('uses theme-aware Apex chrome instead of forcing dark mode', () => {
    const wrapper = mount(TrafficRateSparkline, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1024 }
        ]
      },
      ...mountOptions
    })

    expect(wrapper.vm.chartOptions.theme).toBeUndefined()
    expect(wrapper.vm.chartOptions.tooltip.theme).toBeUndefined()
    expect(wrapper.vm.chartOptions.chart.foreColor).toBe('var(--color-text-secondary)')
    expect(wrapper.vm.chartOptions.colors).toEqual(['var(--color-primary)'])
  })
})
