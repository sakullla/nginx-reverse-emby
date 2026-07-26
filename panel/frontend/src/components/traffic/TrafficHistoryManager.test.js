import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficHistoryManager from './TrafficHistoryManager.vue'

describe('TrafficHistoryManager', () => {
  const mountedWrappers = []

  function mountManager(props = {}) {
    const wrapper = mount(TrafficHistoryManager, {
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
    mountedWrappers.push(wrapper)
    return wrapper
  }

  afterEach(() => {
    while (mountedWrappers.length) {
      mountedWrappers.pop().unmount()
    }
  })

  it('emits each history action from the matching button', async () => {
    const wrapper = mountManager()
    const buttons = wrapper.findAll('.traffic-history-manager__actions button')
    await buttons[0].trigger('click')
    await buttons[1].trigger('click')
    await buttons[2].trigger('click')

    expect(wrapper.emitted('calibrate')).toHaveLength(1)
    expect(wrapper.emitted('calibrate-zero')).toHaveLength(1)
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
