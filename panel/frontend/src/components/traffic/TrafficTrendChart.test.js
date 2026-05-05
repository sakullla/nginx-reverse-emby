import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficTrendChart from './TrafficTrendChart.vue'

const ApexChartStub = {
  name: 'apexchart',
  template: '<div data-testid="apexchart" />',
  props: ['type', 'options', 'series', 'height', 'width']
}

const mountOptions = {
  global: {
    stubs: {
      apexchart: ApexChartStub
    }
  }
}

describe('TrafficTrendChart', () => {
  it('renders apexchart component', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
        ]
      },
      ...mountOptions
    })
    expect(wrapper.find('[data-testid="apexchart"]').exists()).toBe(true)
  })

  it('computes series from points prop', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 },
          { bucket_start: '2026-05-02T00:00:00Z', accounted_bytes: 2000, rx_bytes: 1200, tx_bytes: 800 }
        ]
      },
      ...mountOptions
    })
    const series = wrapper.vm.series
    expect(series.length).toBeGreaterThanOrEqual(3)
    expect(series[0].name).toBe('用量')
    expect(series[0].data).toEqual([1000, 2000])
    expect(series[1].name).toBe('RX')
    expect(series[2].name).toBe('TX')
  })

  it('includes host traffic series when hostPoints provided', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
        ],
        hostPoints: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1500, rx_bytes: 900, tx_bytes: 600 }
        ]
      },
      ...mountOptions
    })
    const hostSeries = wrapper.vm.series.find(s => s.name === '主机流量')
    expect(hostSeries).toBeDefined()
    expect(hostSeries.data).toEqual([1500])
  })

  it('formats x-axis labels for day granularity', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
        ],
        granularity: 'day'
      },
      ...mountOptions
    })
    expect(wrapper.vm.labels.length).toBe(1)
    expect(wrapper.vm.labels[0]).toContain('5')
  })

  it('formats x-axis labels for hour granularity', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T08:30:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
        ],
        granularity: 'hour'
      },
      ...mountOptions
    })
    expect(wrapper.vm.labels[0]).toMatch(/\d{2}:\d{2}/)
  })
})
