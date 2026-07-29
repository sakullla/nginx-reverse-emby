import { afterEach, describe, expect, it } from 'vitest'
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

const basePoint = {
  bucket_start: '2026-05-01T00:00:00Z',
  accounted_bytes: 1000,
  rx_bytes: 600,
  tx_bytes: 400
}

const mountedWrappers = []

function mountChart(props = {}, options = {}) {
  const wrapper = mount(TrafficTrendChart, {
    props: {
      points: [basePoint],
      ...props
    },
    ...mountOptions,
    ...options
  })
  mountedWrappers.push(wrapper)
  return wrapper
}

afterEach(() => {
  while (mountedWrappers.length) {
    mountedWrappers.pop().unmount()
  }
  document.body.innerHTML = ''
})

describe('TrafficTrendChart', () => {
  it('switches between chart, empty, and loading states', async () => {
    const wrapper = mountChart()
    expect(wrapper.find('[data-testid="apexchart"]').exists()).toBe(true)

    await wrapper.setProps({ points: [] })
    expect(wrapper.find('[data-testid="traffic-trend-empty"]').text()).toContain('暂无数据')
    expect(wrapper.find('[data-testid="apexchart"]').exists()).toBe(false)

    await wrapper.setProps({ points: [basePoint], loading: true })
    expect(wrapper.find('[data-testid="traffic-trend-loading"]').text()).toContain('加载中')
    expect(wrapper.find('[data-testid="apexchart"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('暂无数据')
  })

  it('uses explicit pixel height and ignores invalid overrides', async () => {
    const wrapper = mountChart({ height: 176 })
    expect(wrapper.findComponent(ApexChartStub).props('height')).toBe(176)
    expect(wrapper.element.style.height).toBe('176px')

    await wrapper.setProps({ height: 'auto' })
    expect(wrapper.findComponent(ApexChartStub).props('height')).toBe(260)
    expect(wrapper.element.style.height).toBe('')
  })

  it('uses measured host height when no override is set', async () => {
    const wrapper = mountChart({}, { attachTo: document.body })
    Object.defineProperty(wrapper.element, 'getBoundingClientRect', {
      configurable: true,
      value: () => ({
        height: 188,
        width: 320,
        top: 0,
        left: 0,
        bottom: 188,
        right: 320,
        x: 0,
        y: 0,
        toJSON: () => ({})
      })
    })

    await wrapper.setProps({ loading: true })
    await wrapper.setProps({ loading: false })
    await wrapper.vm.$nextTick()

    const height = wrapper.findComponent(ApexChartStub).props('height')
    expect(Number(height)).toBeGreaterThan(0)
    expect(String(height)).not.toContain('%')
  })

  it('builds usage series and styles the quota threshold independently', () => {
    const wrapper = mountChart({
      points: [
        basePoint,
        { bucket_start: '2026-05-02T00:00:00Z', accounted_bytes: 2000, rx_bytes: 1200, tx_bytes: 800 }
      ],
      quotaBytes: 2000,
      granularity: 'month'
    })

    const seriesNames = wrapper.vm.series.map((item) => item.name)
    const quotaIndex = seriesNames.indexOf('月额度')
    expect(seriesNames).toEqual(['用量', 'RX', 'TX', '月额度'])
    expect(wrapper.vm.series[0].data).toEqual([1000, 2000])
    expect(wrapper.vm.series[1].data).toEqual([600, 1200])
    expect(wrapper.vm.series[2].data).toEqual([400, 800])
    expect(wrapper.vm.chartOptions.stroke.width).toHaveLength(seriesNames.length)
    expect(wrapper.vm.chartOptions.colors[quotaIndex]).toBe('#ef4444')
    expect(wrapper.vm.chartOptions.stroke.width[quotaIndex]).toBe(1)
    expect(wrapper.vm.chartOptions.stroke.dashArray[quotaIndex]).toBe(6)
    expect(wrapper.vm.chartOptions.fill.type[quotaIndex]).toBe('none')
    expect(wrapper.vm.chartOptions.fill.opacity[quotaIndex]).toBe(0)
  })

  it('uses panel-local labels without merging backend buckets at any granularity', async () => {
    const wrapper = mountChart({
      granularity: 'day',
      points: [
        { bucket_start: '2026-05-04T16:00:00Z', bucket_local_start: '2026-05-05T00:00:00+08:00', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 },
        { bucket_start: '2026-05-05T16:00:00Z', bucket_local_start: '2026-05-06T00:00:00+08:00', accounted_bytes: 2000, rx_bytes: 1200, tx_bytes: 800 }
      ]
    })
    expect(wrapper.vm.labels).toEqual(['5月5日', '5月6日'])
    expect(wrapper.vm.series[0].data).toEqual([1000, 2000])

    await wrapper.setProps({
      granularity: 'hour',
      points: [{ ...basePoint, bucket_local_start: '2026-05-01T08:30:00+08:00' }]
    })
    expect(wrapper.vm.labels).toEqual(['08:30'])

    await wrapper.setProps({
      granularity: 'month',
      points: [
        { bucket_start: '2026-04-30T16:00:00Z', bucket_local_start: '2026-05-01T00:00:00+08:00', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 },
        { bucket_start: '2026-05-31T16:00:00Z', bucket_local_start: '2026-06-01T00:00:00+08:00', accounted_bytes: 2000, rx_bytes: 1200, tx_bytes: 800 }
      ]
    })
    expect(wrapper.vm.labels).toEqual(['26年5月', '26年6月'])
    expect(wrapper.vm.series[0].data).toEqual([1000, 2000])
  })

  it('keeps byte formatters and theme-aware chart chrome', () => {
    const wrapper = mountChart({
      points: [{
        bucket_start: '2026-05-01T00:00:00Z',
        accounted_bytes: 9481461104,
        rx_bytes: 9481461104,
        tx_bytes: 8375186227
      }]
    })
    const options = wrapper.vm.chartOptions

    expect(options.yaxis.labels.formatter(10000000000)).toMatch(/GiB$/)
    expect(options.yaxis.labels.formatter('10000000000')).toMatch(/GiB$/)
    expect(options.yaxis.labels.formatter(null)).toBe('')
    expect(options.tooltip.y.formatter(10000000000)).toMatch(/GiB$/)
    expect(options.theme).toBeUndefined()
    expect(options.chart.foreColor).toBe('var(--color-text-secondary)')
    expect(options.grid.borderColor).toBe('var(--color-border-subtle)')
  })

  it('remounts for changed or refetched point arrays', async () => {
    const wrapper = mountChart()
    const initialKey = wrapper.findComponent(ApexChartStub).vm.$.vnode.key

    const changedPoints = [
      { bucket_start: '2026-05-02T00:00:00Z', accounted_bytes: 1000, rx_bytes: 700, tx_bytes: 300 }
    ]
    await wrapper.setProps({ points: changedPoints })
    const changedKey = wrapper.findComponent(ApexChartStub).vm.$.vnode.key
    expect(changedKey).not.toBe(initialKey)

    await wrapper.setProps({ points: changedPoints.map((point) => ({ ...point })) })
    expect(wrapper.findComponent(ApexChartStub).vm.$.vnode.key).not.toBe(changedKey)
  })

  it('remounts each granularity on refresh without rebuilding formatter options', async () => {
    const points = [{
      bucket_start: '2026-05-01T00:00:00Z',
      accounted_bytes: 9481461104,
      rx_bytes: 9481461104,
      tx_bytes: 8375186227
    }]
    const wrapper = mountChart({ points })

    for (const granularity of ['hour', 'day', 'month']) {
      await wrapper.setProps({ granularity, points, refreshKey: `${granularity}-1` })
      const initialChart = wrapper.findComponent(ApexChartStub)
      const initialKey = initialChart.vm.$.vnode.key
      const initialOptions = initialChart.props('options')

      await wrapper.setProps({ refreshKey: `${granularity}-2` })

      const nextChart = wrapper.findComponent(ApexChartStub)
      const nextOptions = nextChart.props('options')
      expect(nextChart.vm.$.vnode.key).not.toBe(initialKey)
      expect(nextOptions).toBe(initialOptions)
      expect(nextOptions.yaxis.labels.formatter(10000000000)).toMatch(/GiB$/)
      expect(nextOptions.tooltip.y.formatter(10000000000)).toMatch(/GiB$/)
    }
  })
})
