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

  it('styles quota threshold series independently of optional series order', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
        ],
        quotaBytes: 2000,
        granularity: 'month'
      },
      ...mountOptions
    })

    const seriesNames = wrapper.vm.series.map((item) => item.name)
    const quotaIndex = seriesNames.indexOf('月额度')

    expect(seriesNames).toEqual(['用量', 'RX', 'TX', '月额度'])
    expect(wrapper.vm.chartOptions.stroke.width).toHaveLength(seriesNames.length)
    expect(wrapper.vm.chartOptions.colors[quotaIndex]).toBe('#ef4444')
    expect(wrapper.vm.chartOptions.stroke.width[quotaIndex]).toBe(1)
    expect(wrapper.vm.chartOptions.stroke.dashArray[quotaIndex]).toBe(6)
    expect(wrapper.vm.chartOptions.fill.type[quotaIndex]).toBe('none')
    expect(wrapper.vm.chartOptions.fill.opacity[quotaIndex]).toBe(0)
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

  it('uses panel-local day labels without merging separate backend buckets', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-04T16:00:00Z', bucket_local_start: '2026-05-05T00:00:00+08:00', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 },
          { bucket_start: '2026-05-05T16:00:00Z', bucket_local_start: '2026-05-06T00:00:00+08:00', accounted_bytes: 2000, rx_bytes: 1200, tx_bytes: 800 }
        ],
        granularity: 'day'
      },
      ...mountOptions
    })

    expect(wrapper.vm.labels).toEqual(['5月5日', '5月6日'])
    expect(wrapper.vm.series[0].data).toEqual([1000, 2000])
    expect(wrapper.vm.series[1].data).toEqual([600, 1200])
    expect(wrapper.vm.series[2].data).toEqual([400, 800])
  })

  it('uses panel-local month labels without merging separate backend buckets', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-04-30T16:00:00Z', bucket_local_start: '2026-05-01T00:00:00+08:00', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 },
          { bucket_start: '2026-05-31T16:00:00Z', bucket_local_start: '2026-06-01T00:00:00+08:00', accounted_bytes: 2000, rx_bytes: 1200, tx_bytes: 800 }
        ],
        granularity: 'month'
      },
      ...mountOptions
    })

    expect(wrapper.vm.labels).toEqual(['26年5月', '26年6月'])
    expect(wrapper.vm.series[0].data).toEqual([1000, 2000])
    expect(wrapper.vm.series[1].data).toEqual([600, 1200])
    expect(wrapper.vm.series[2].data).toEqual([400, 800])
  })

  it('formats y-axis labels with byte units', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 9481461104, rx_bytes: 9481461104, tx_bytes: 8375186227 }
        ]
      },
      ...mountOptions
    })

    const formatter = wrapper.vm.chartOptions.yaxis.labels.formatter

    expect(formatter(10000000000)).toMatch(/GiB$/)
    expect(formatter('10000000000')).toMatch(/GiB$/)
    expect(formatter(null)).toBe('')
  })

  it('formats tooltip values with the same byte unit formatter', () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 9481461104, rx_bytes: 9481461104, tx_bytes: 8375186227 }
        ]
      },
      ...mountOptions
    })

    expect(wrapper.vm.chartOptions.tooltip.y.formatter(10000000000)).toMatch(/GiB$/)
  })

  it('remounts apexchart when same-size point data changes so formatter functions are not stripped by updates', async () => {
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points: [
          { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
        ]
      },
      ...mountOptions
    })
    const initialKey = wrapper.findComponent(ApexChartStub).vm.$.vnode.key

    await wrapper.setProps({
      points: [
        { bucket_start: '2026-05-02T00:00:00Z', accounted_bytes: 1000, rx_bytes: 700, tx_bytes: 300 }
      ]
    })

    expect(wrapper.findComponent(ApexChartStub).vm.$.vnode.key).not.toBe(initialKey)
  })

  it('remounts apexchart when an external refresh key changes without point changes', async () => {
    const points = [
      { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
    ]
    const wrapper = mount(TrafficTrendChart, {
      props: {
        points,
        refreshKey: 1
      },
      ...mountOptions
    })
    const initialKey = wrapper.findComponent(ApexChartStub).vm.$.vnode.key

    await wrapper.setProps({
      points,
      refreshKey: 2
    })

    expect(wrapper.findComponent(ApexChartStub).vm.$.vnode.key).not.toBe(initialKey)
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
