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

  it('renders retention policy summary and action buttons', () => {
    const wrapper = mountManager()
    expect(wrapper.find('.traffic-history-manager__summary').exists()).toBe(true)
    expect(wrapper.text()).toContain('小时 30 天')
    expect(wrapper.text()).toContain('日 3 个月')
    expect(wrapper.text()).toContain('月 36 个月')

    const buttons = wrapper.findAll('.traffic-history-manager__actions button')
    expect(buttons.length).toBe(3)
    expect(buttons[0].text()).toBe('校准为指定值')
    expect(buttons[1].text()).toBe('从现在归零')
    expect(buttons[2].text()).toBe('清理过期数据')
  })

  it('emits calibrate on calibrate button click', async () => {
    const wrapper = mountManager()
    const buttons = wrapper.findAll('.traffic-history-manager__actions button')
    await buttons[0].trigger('click')
    expect(wrapper.emitted('calibrate')).toHaveLength(1)
  })

  it('emits calibrate-zero on zero button click', async () => {
    const wrapper = mountManager()
    const buttons = wrapper.findAll('.traffic-history-manager__actions button')
    await buttons[1].trigger('click')
    expect(wrapper.emitted('calibrate-zero')).toHaveLength(1)
  })

  it('emits cleanup on cleanup button click', async () => {
    const wrapper = mountManager()
    const buttons = wrapper.findAll('.traffic-history-manager__actions button')
    await buttons[2].trigger('click')
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
