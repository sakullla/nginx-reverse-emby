import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficBreakdownTable from './TrafficBreakdownTable.vue'

const sampleRows = [
  {
    scope_type: 'http_rule',
    scope_id: 1,
    accounted_bytes: 750,
    rx_bytes: 500,
    tx_bytes: 250
  },
  {
    scope_type: 'http_rule',
    scope_id: 2,
    accounted_bytes: 250,
    rx_bytes: 100,
    tx_bytes: 150
  }
]

describe('TrafficBreakdownTable', () => {
  function mountTable(props = {}) {
    return mount(TrafficBreakdownTable, {
      props: {
        rows: sampleRows,
        ...props
      }
    })
  }

  it('renders name and usage as primary columns', () => {
    const wrapper = mountTable()
    expect(wrapper.find('.traffic-breakdown__name').text()).toBe('HTTP 规则 #1')
    expect(wrapper.find('.traffic-breakdown__value').exists()).toBe(true)
    expect(wrapper.text()).toContain('HTTP 规则 #1')
    expect(wrapper.find('.traffic-breakdown__th--usage').text()).toContain('用量')
  })

  it('renders share percent and bar for each row', () => {
    const wrapper = mountTable()
    const shares = wrapper.findAll('.traffic-breakdown__share')
    expect(shares.length).toBe(2)
    expect(wrapper.findAll('.traffic-breakdown__share-bar').length).toBe(2)
    expect(wrapper.findAll('.traffic-breakdown__percent').map((n) => n.text())).toEqual(['75%', '25%'])
    const firstBar = wrapper.find('.traffic-breakdown__share-bar')
    expect(firstBar.attributes('style')).toContain('width:')
  })

  it('merges RX/TX into muted secondary io column without dropping values', () => {
    const wrapper = mountTable()
    expect(wrapper.find('.traffic-breakdown__th--io').text()).toBe('收发')
    expect(wrapper.findAll('.traffic-breakdown__raw').length).toBe(0)
    const io = wrapper.find('.traffic-breakdown__io')
    expect(io.exists()).toBe(true)
    expect(io.text()).toContain('RX')
    expect(io.text()).toContain('TX')
  })

  it('shows empty state when no rows', () => {
    const wrapper = mountTable({ rows: [] })
    expect(wrapper.find('.traffic-breakdown__empty').text()).toBe('暂无分项流量')
    expect(wrapper.findAll('.traffic-breakdown__row').length).toBe(0)
  })

  it('defaults to accounted_bytes descending order', () => {
    const wrapper = mountTable({
      rows: [
        { scope_type: 'http_rule', scope_id: 9, accounted_bytes: 10, rx_bytes: 1, tx_bytes: 1 },
        { scope_type: 'http_rule', scope_id: 8, accounted_bytes: 90, rx_bytes: 1, tx_bytes: 1 }
      ]
    })
    const names = wrapper.findAll('.traffic-breakdown__name').map((n) => n.text())
    expect(names[0]).toBe('HTTP 规则 #8')
    expect(names[1]).toBe('HTTP 规则 #9')
    expect(wrapper.find('.traffic-breakdown__th--usage').text()).toContain('↓')
  })

  it('emits click-row when clickable', async () => {
    const wrapper = mountTable({ clickable: true })
    await wrapper.find('.traffic-breakdown__row').trigger('click')
    expect(wrapper.emitted('click-row')).toHaveLength(1)
    expect(wrapper.emitted('click-row')[0][0].scope_id).toBe(1)
  })
})
