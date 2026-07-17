import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
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

  it('formats metric values from summary and network metrics', () => {
    const wrapper = mountCards({
      networkMetrics: {
        rx_bytes_per_second: 1024,
        tx_bytes_per_second: 2048
      }
    })
    expect(wrapper.get('[data-testid="traffic-summary-used"]').text()).toContain('1.00 GiB')
    expect(wrapper.get('[data-testid="traffic-summary-remaining"]').text()).toContain('2.00 GiB')
    expect(wrapper.get('[data-testid="traffic-summary-up"]').text()).toContain('512.0 MiB')
    expect(wrapper.get('[data-testid="traffic-summary-down"]').text()).toContain('2.00 GiB')
    // Current rate splits download (rx) and upload (tx) onto separate rows.
    const rateRows = wrapper.findAll('[data-testid="traffic-summary-card-rate-row"]')
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
    expect(wrapper.findAll('[data-testid="traffic-summary-card-rate-row"]').length).toBe(0)
  })

  it('renders only the available direction in the current-rate card', () => {
    const wrapper = mountCards({
      networkMetrics: { rx_bytes_per_second: 1024 }
    })
    const rateRows = wrapper.findAll('[data-testid="traffic-summary-card-rate-row"]')
    expect(rateRows).toHaveLength(1)
    expect(rateRows[0].text()).toContain('↓')
    expect(rateRows[0].text()).toContain('1.00 KiB/s')
  })

  it('shows loading placeholder and never treats empty summary as 无限制 while loading', () => {
    const wrapper = mountCards({
      loading: true,
      summary: {}
    })
    expect(wrapper.get('[data-testid="traffic-summary-loading"]').text()).toContain('加载中')
    expect(wrapper.text()).not.toContain('无限制')
    expect(wrapper.find('[data-testid="traffic-summary-remaining"]').exists()).toBe(false)
    expect(wrapper.findAll('.traffic-summary-card__metric').length).toBe(0)
  })

  it('still shows 无限制 when loaded with null quota even if summary fields are sparse', () => {
    const wrapper = mountCards({
      loading: false,
      summary: {
        used_bytes: 0,
        rx_bytes: 0,
        tx_bytes: 0,
        monthly_quota_bytes: null,
        remaining_bytes: null,
        blocked: false
      }
    })
    expect(wrapper.find('[data-testid="traffic-summary-loading"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="traffic-summary-remaining"]').text()).toBe('无限制')
  })

  it('exposes analysis/management actions on total and remaining primaries', async () => {
    const wrapper = mountCards()
    const analysis = wrapper.get('[data-testid="traffic-summary-open-analysis"]')
    const management = wrapper.get('[data-testid="traffic-summary-open-management"]')
    expect(analysis.text()).toBe('分析')
    expect(management.text()).toBe('管理')
    expect(analysis.element.disabled).toBe(false)
    expect(management.element.disabled).toBe(false)

    await analysis.trigger('click')
    await management.trigger('click')
    expect(wrapper.emitted('open-analysis')).toHaveLength(1)
    expect(wrapper.emitted('open-management')).toHaveLength(1)

    expect(wrapper.get('[data-testid="traffic-summary-used"]').text()).toContain('1.00 GiB')
    expect(wrapper.get('[data-testid="traffic-summary-remaining"]').text()).toContain('2.00 GiB')
  })

  it('does not mount scenario actions while loading', () => {
    const wrapper = mountCards({
      loading: true,
      summary: {}
    })
    expect(wrapper.find('[data-testid="traffic-summary-open-analysis"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="traffic-summary-open-management"]').exists()).toBe(false)
  })
})
