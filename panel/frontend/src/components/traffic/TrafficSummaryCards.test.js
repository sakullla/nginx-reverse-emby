import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficSummaryCards from './TrafficSummaryCards.vue'

describe('TrafficSummaryCards', () => {
  it('labels host_total as current-cycle host traffic instead of 24h bandwidth', () => {
    const wrapper = mount(TrafficSummaryCards, {
      props: {
        direction: 'both',
        summary: {
          used_bytes: 1024,
          monthly_quota_bytes: null,
          remaining_bytes: null
        },
        hostTotal: {
          scope_type: 'host_total',
          rx_bytes: 1024,
          tx_bytes: 2048,
          accounted_bytes: 3072
        }
      }
    })

    const hostCard = wrapper.findAll('.traffic-summary-card')
      .find(card => card.text().includes('主机流量'))
    expect(hostCard?.exists()).toBe(true)
    expect(hostCard.text()).toContain('当前周期')
    expect(hostCard.text()).not.toContain('24h')
    expect(hostCard.text()).toContain('3.00 KiB')
  })
})
