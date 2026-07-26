import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficPolicyForm from './TrafficPolicyForm.vue'

describe('TrafficPolicyForm', () => {
  const mountedWrappers = []

  function mountForm(props = {}) {
    const wrapper = mount(TrafficPolicyForm, {
      props: {
        modelValue: {
          direction: 'both',
          cycle_start_day: 1,
          monthly_quota_value: '',
          monthly_quota_unit: 'GiB',
          block_when_exceeded: false,
          hourly_retention_days: 30,
          daily_retention_months: 3,
          monthly_retention_months: 36,
          traffic_stats_interval: ''
        },
        saving: false,
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

  it('emits update:modelValue on field change', async () => {
    const wrapper = mountForm()
    const input = wrapper.find('[data-testid="traffic-policy-card-retention"]')
      .findAll('input')[0]
    await input.setValue('60')
    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    const last = wrapper.emitted('update:modelValue').at(-1)[0]
    expect(last.hourly_retention_days).toBe(60)
  })

  it('emits save on button click', async () => {
    const wrapper = mountForm()
    await wrapper.find('.traffic-policy-form__save').trigger('click')
    expect(wrapper.emitted('save')).toHaveLength(1)
  })
})
