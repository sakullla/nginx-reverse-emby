import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import TrafficTrendChart from './TrafficTrendChart.vue'

const chartCtor = vi.fn()
const chartDestroy = vi.fn()

vi.mock('chart.js', () => ({
  Chart: Object.assign(vi.fn().mockImplementation((ctx, config) => {
    chartCtor(ctx, config)
    return { destroy: chartDestroy }
  }), { register: vi.fn() }),
  registerables: []
}))

describe('TrafficTrendChart', () => {
  it('keeps host-only buckets in the host series', async () => {
    const originalUserAgent = navigator.userAgent
    const originalGetContext = HTMLCanvasElement.prototype.getContext
    Object.defineProperty(navigator, 'userAgent', {
      configurable: true,
      value: 'unit-test'
    })
    HTMLCanvasElement.prototype.getContext = vi.fn(() => ({}))

    try {
      mount(TrafficTrendChart, {
        props: {
          points: [
            { bucket_start: '2026-05-02T00:00:00Z', accounted_bytes: 100, rx_bytes: 0, tx_bytes: 0 }
          ],
          hostPoints: [
            { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 200 },
            { bucket_start: '2026-05-02T00:00:00Z', accounted_bytes: 300 }
          ],
          granularity: 'day'
        }
      })

      await nextTick()

      expect(chartCtor).toHaveBeenCalled()
      const config = chartCtor.mock.calls.at(-1)[1]
      const hostDataset = config.data.datasets.find((dataset) => dataset.label === '主机流量')
      expect(config.data.labels).toEqual(['5月1日', '5月2日'])
      expect(hostDataset.data).toEqual([200, 300])
    } finally {
      HTMLCanvasElement.prototype.getContext = originalGetContext
      Object.defineProperty(navigator, 'userAgent', {
        configurable: true,
        value: originalUserAgent
      })
    }
  })
})
