import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseInput from './BaseInput.vue'

describe('BaseInput', () => {
  it('wires label, aria-invalid and aria-describedby when an error is present', async () => {
    const wrapper = mount(BaseInput, {
      props: { modelValue: '', label: '名称', id: 'rule-name', error: '名称不能为空' }
    })
    const input = wrapper.get('input')
    expect(wrapper.get('label').attributes('for')).toBe('rule-name')
    expect(input.attributes('id')).toBe('rule-name')
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(input.attributes('aria-describedby')).toBe('rule-name-error')
    expect(wrapper.get('[role="alert"]').text()).toContain('名称不能为空')

    await wrapper.setProps({ error: '' })
    expect(input.attributes('aria-invalid')).toBeUndefined()
    expect(input.attributes('aria-describedby')).toBeUndefined()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('keeps emitting updates and generates a stable id when none is given', async () => {
    const wrapper = mount(BaseInput, { props: { modelValue: '' } })
    const input = wrapper.get('input')
    expect(input.attributes('id')).toBeTruthy()
    await input.setValue('abc')
    expect(wrapper.emitted('update:modelValue')).toEqual([['abc']])
  })
})
