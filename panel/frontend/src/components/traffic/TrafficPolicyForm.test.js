import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficPolicyForm from './TrafficPolicyForm.vue'

describe('TrafficPolicyForm', () => {
  function mountForm(props = {}) {
    return mount(TrafficPolicyForm, {
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
  }

  it('renders three card sections', () => {
    const wrapper = mountForm()
    const cards = wrapper.findAll('.traffic-policy-form__card')
    expect(cards.length).toBe(3)
    expect(wrapper.text()).toContain('计费配置')
    expect(wrapper.text()).toContain('数据保留策略')
    expect(wrapper.text()).toContain('高级设置')
  })

  it('shows retention unit badges', () => {
    const wrapper = mountForm()
    expect(wrapper.text()).toContain('单位：天')
    expect(wrapper.text()).toContain('单位：月')
    expect(wrapper.text()).toContain('约 1 个月')
    expect(wrapper.text()).toContain('约 90 天')
    expect(wrapper.text()).toContain('约 3 年')
  })

  it('emits update:modelValue on field change', async () => {
    const wrapper = mountForm()
    const input = wrapper.findAll('.traffic-policy-form__card')[1]
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
