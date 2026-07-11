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

  it('renders remaining-scene sections with quota/block first', () => {
    const wrapper = mountForm()
    const titles = wrapper.findAll('.traffic-policy-form__card-title').map((el) => el.text())
    expect(titles).toEqual(['额度与阻断', '计费方向与周期', '数据保留策略', '高级设置'])
    expect(wrapper.find('[data-testid="traffic-policy-card-quota"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-policy-card-quota"].traffic-policy-form__card--primary').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-policy-card-quota"]').text()).toContain('优先')
    expect(wrapper.find('[data-testid="traffic-policy-card-billing"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-policy-card-retention"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-policy-card-advanced"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="traffic-policy-card-advanced"].traffic-policy-form__card--muted').exists()).toBe(true)
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
