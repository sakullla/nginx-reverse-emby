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

  it('renders a single KPI card with five metrics', () => {
    const wrapper = mountCards()
    expect(wrapper.find('.traffic-summary-cards').exists()).toBe(true)
    expect(wrapper.find('.traffic-summary-cards__grid').exists()).toBe(true)
    const metrics = wrapper.findAll('.traffic-summary-card__metric')
    expect(metrics.length).toBe(5)
  })

  it('labels metrics as 总流量 / 剩余 / 上行 / 下行 / 当前速率', () => {
    const wrapper = mountCards()
    const labels = wrapper.findAll('.traffic-summary-card__label').map((el) => el.text())
    expect(labels).toEqual(['总流量', '剩余', '上行', '下行', '当前速率'])
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
    expect(values[1]).toContain('2.00 GiB')
    expect(values[2]).toContain('512.0 MiB')
    expect(values[3]).toContain('2.00 GiB')
    // Current rate splits download (rx) and upload (tx) onto separate rows
    // so the value no longer wraps/breaks mid-token in a narrow card.
    const rateCard = wrapper.findAll('.traffic-summary-card__metric').at(4)
    const rateRows = rateCard.findAll('[data-testid="traffic-summary-card-rate-row"]')
    expect(rateRows).toHaveLength(2)
    expect(rateRows[0].text()).toContain('↓')
    expect(rateRows[0].text()).toContain('1.00 KiB/s')
    expect(rateRows[1].text()).toContain('↑')
    expect(rateRows[1].text()).toContain('2.00 KiB/s')
  })

  it('shows remaining_bytes when quota is set', () => {
    const wrapper = mountCards()
    expect(wrapper.get('[data-testid="traffic-summary-remaining"]').text()).toContain('2.00 GiB')
    expect(wrapper.get('[data-testid="traffic-summary-used-sub"]').text()).toContain('占额度')
    expect(wrapper.get('[data-testid="traffic-summary-used-sub"]').text()).toContain('额度')
  })

  it('shows 无限制 when monthly_quota_bytes is null', () => {
    const wrapper = mountCards({
      summary: {
        used_bytes: 1073741824,
        rx_bytes: 2147483648,
        tx_bytes: 536870912,
        monthly_quota_bytes: null,
        remaining_bytes: null,
        blocked: false
      }
    })
    expect(wrapper.get('[data-testid="traffic-summary-remaining"]').text()).toBe('无限制')
    expect(wrapper.get('[data-testid="traffic-summary-remaining-sub"]').text()).toContain('未设置月额度')
    expect(wrapper.find('[data-testid="traffic-summary-used-sub"]').exists()).toBe(false)
    // Must not invent a remaining numeric string when unlimited
    expect(wrapper.get('[data-testid="traffic-summary-remaining"]').text()).not.toMatch(/\d+\s*(B|KiB|MiB|GiB|TiB)/)
  })

  it('shows a dash for current rate when network metrics are unavailable', () => {
    const wrapper = mountCards()
    const rateValue = wrapper.findAll('.traffic-summary-card__value').at(4)
    expect(rateValue.text()).toBe('—')
  })

  it('renders only the available direction in the current-rate card', () => {
    const wrapper = mountCards({
      networkMetrics: { rx_bytes_per_second: 1024 }
    })
    const rateCard = wrapper.findAll('.traffic-summary-card__metric').at(4)
    const rateRows = rateCard.findAll('[data-testid="traffic-summary-card-rate-row"]')
    expect(rateRows).toHaveLength(1)
    expect(rateRows[0].text()).toContain('↓')
    expect(rateRows[0].text()).toContain('1.00 KiB/s')
  })

  it('renders an icon for each metric', () => {
    const wrapper = mountCards()
    const metrics = wrapper.findAll('.traffic-summary-card__metric')
    expect(metrics.length).toBe(5)
    for (const metric of metrics) {
      expect(metric.find('.traffic-summary-card__icon').exists()).toBe(true)
    }
  })

  it('keeps total and remaining as equal primary metrics without hero emphasis', () => {
    const wrapper = mountCards()
    const metrics = wrapper.findAll('.traffic-summary-card__metric')
    expect(metrics[0].classes()).toContain('traffic-summary-card__metric--primary')
    expect(metrics[1].classes()).toContain('traffic-summary-card__metric--primary')
    expect(metrics[1].classes()).not.toContain('traffic-summary-card__metric--hero')
    expect(metrics.every((metric) => !metric.classes().includes('traffic-summary-card__metric--hero'))).toBe(true)
  })

  it('marks uplink/downlink/current-rate as a secondary block', () => {
    const wrapper = mountCards()
    const metrics = wrapper.findAll('.traffic-summary-card__metric')
    expect(metrics[2].classes()).toContain('traffic-summary-card__metric--secondary')
    expect(metrics[3].classes()).toContain('traffic-summary-card__metric--secondary')
    expect(metrics[4].classes()).toContain('traffic-summary-card__metric--secondary')
  })

  it('uses equal primary tracks and equal secondary rate tracks on desktop', () => {
    const source = readFileSync(resolve(process.cwd(), 'src/components/traffic/TrafficSummaryCards.vue'), 'utf8')
    expect(source).toContain(
      'grid-template-columns: minmax(0, 1.1fr) minmax(0, 1.1fr) minmax(0, 0.95fr) minmax(0, 0.95fr) minmax(0, 0.95fr);'
    )
    expect(source).not.toContain('traffic-summary-card__metric--hero')
  })
})
