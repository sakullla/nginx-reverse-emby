import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficHistoryManager from './TrafficHistoryManager.vue'

describe('TrafficHistoryManager', () => {
  function mountManager(props = {}) {
    return mount(TrafficHistoryManager, {
      props: {
        policy: {
          hourly_retention_days: 30,
          daily_retention_months: 3,
          monthly_retention_months: 36
        },
        calibrating: false,
        cleaning: false,
        ...props
      }
    })
  }

  it('renders two action cards', () => {
    const wrapper = mountManager()
    const cards = wrapper.findAll('.traffic-history-manager__card')
    expect(cards.length).toBe(2)
    expect(wrapper.text()).toContain('流量校准')
    expect(wrapper.text()).toContain('数据清理')
  })

  it('displays retention policy summary in cleanup card', () => {
    const wrapper = mountManager()
    expect(wrapper.text()).toContain('小时 30 天')
    expect(wrapper.text()).toContain('日 3 个月')
    expect(wrapper.text()).toContain('月 36 个月')
  })

  it('emits calibrate on calibrate button click', async () => {
    const wrapper = mountManager()
    const calibrateBtn = wrapper.findAll('.traffic-history-manager__card')[0]
      .find('button')
    await calibrateBtn.trigger('click')
    expect(wrapper.emitted('calibrate')).toHaveLength(1)
  })

  it('emits cleanup on cleanup button click', async () => {
    const wrapper = mountManager()
    const cleanupBtn = wrapper.findAll('.traffic-history-manager__card')[1]
      .find('button')
    await cleanupBtn.trigger('click')
    expect(wrapper.emitted('cleanup')).toHaveLength(1)
  })

  it('disables buttons when loading', () => {
    const wrapper = mountManager({ calibrating: true, cleaning: true })
    const buttons = wrapper.findAll('button')
    for (const btn of buttons) {
      expect(btn.attributes('disabled')).toBeDefined()
    }
  })

  it('renders with null monthly retention months', () => {
    const wrapper = mountManager({
      policy: {
        hourly_retention_days: 30,
        daily_retention_months: 3,
        monthly_retention_months: null
      }
    })
    expect(wrapper.text()).toContain('月 — 个月')
  })
})
