import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import TrafficSummaryCards from './TrafficSummaryCards.vue'

describe('TrafficSummaryCards', () => {
  function mountCards(props = {}) {
    return mount(TrafficSummaryCards, {
      props: {
        summary: {
          used_bytes: 1073741824,
          rx_bytes: 2147483648,
          tx_bytes: 536870912,
          monthly_quota_bytes: 3221225472,
          remaining_bytes: 2147483648,
          blocked: false
        },
        direction: 'both',
        networkMetrics: null,
        ...props
      }
    })
  }

  it('renders a single KPI card with four metrics', () => {
    const wrapper = mountCards()
    expect(wrapper.find('.traffic-summary-cards').exists()).toBe(true)
    expect(wrapper.find('.traffic-summary-cards__grid').exists()).toBe(true)
    const metrics = wrapper.findAll('.traffic-summary-card__metric')
    expect(metrics.length).toBe(4)
  })

  it('labels metrics as 总流量 / 上行 / 下行 / 当前速率', () => {
    const wrapper = mountCards()
    const labels = wrapper.findAll('.traffic-summary-card__label').map((el) => el.text())
    expect(labels).toEqual(['总流量', '上行', '下行', '当前速率'])
  })

  it('formats metric values from summary and network metrics', () => {
    const wrapper = mountCards({
      networkMetrics: {
        rx_bytes_per_second: 1024,
        tx_bytes_per_second: 2048
      }
    })
    const values = wrapper.findAll('.traffic-summary-card__value').map((el) => el.text())
    expect(values[0]).toContain('1.00 GiB')
    expect(values[1]).toContain('512.0 MiB')
    expect(values[2]).toContain('2.00 GiB')
    // Current rate splits download (rx) and upload (tx) onto separate rows
    // so the value no longer wraps/breaks mid-token in a narrow card.
    const rateCard = wrapper.findAll('.traffic-summary-card__metric').at(3)
    const rateRows = rateCard.findAll('[data-testid="traffic-summary-card-rate-row"]')
    expect(rateRows).toHaveLength(2)
    expect(rateRows[0].text()).toContain('↓')
    expect(rateRows[0].text()).toContain('1.00 KiB/s')
    expect(rateRows[1].text()).toContain('↑')
    expect(rateRows[1].text()).toContain('2.00 KiB/s')
  })

  it('shows a dash for current rate when network metrics are unavailable', () => {
    const wrapper = mountCards()
    const rateValue = wrapper.findAll('.traffic-summary-card__value').at(3)
    expect(rateValue.text()).toBe('—')
  })

  it('renders only the available direction in the current-rate card', () => {
    const wrapper = mountCards({
      networkMetrics: { rx_bytes_per_second: 1024 }
    })
    const rateCard = wrapper.findAll('.traffic-summary-card__metric').at(3)
    const rateRows = rateCard.findAll('[data-testid="traffic-summary-card-rate-row"]')
    expect(rateRows).toHaveLength(1)
    expect(rateRows[0].text()).toContain('↓')
    expect(rateRows[0].text()).toContain('1.00 KiB/s')
  })

  it('renders an icon for each metric', () => {
    const wrapper = mountCards()
    const metrics = wrapper.findAll('.traffic-summary-card__metric')
    expect(metrics.length).toBe(4)
    for (const metric of metrics) {
      expect(metric.find('.traffic-summary-card__icon').exists()).toBe(true)
    }
  })

  it('highlights total and current-rate metrics', () => {
    const wrapper = mountCards()
    const metrics = wrapper.findAll('.traffic-summary-card__metric')
    expect(metrics[0].classes()).toContain('traffic-summary-card__metric--primary')
    expect(metrics[3].classes()).toContain('traffic-summary-card__metric--primary')
  })

  it('uses a four-column desktop grid', () => {
    const source = readFileSync(resolve(process.cwd(), 'src/components/traffic/TrafficSummaryCards.vue'), 'utf8')
    expect(source).toContain('grid-template-columns: repeat(4, 1fr);')
  })
})
