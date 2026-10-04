import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import FieldError from './FieldError.vue'

describe('FieldError', () => {
  it('announces the message as an alert and links by id for aria-describedby', () => {
    const wrapper = mount(FieldError, {
      props: { id: 'name-error' },
      slots: { default: '名称不能为空' }
    })
    const el = wrapper.get('p.form-error')
    expect(el.attributes('role')).toBe('alert')
    expect(el.attributes('id')).toBe('name-error')
    expect(el.text()).toContain('名称不能为空')
    expect(el.get('svg').attributes('aria-hidden')).toBe('true')
  })

  it('omits id when not provided and supports the block variant', () => {
    const wrapper = mount(FieldError, { props: { block: true }, slots: { default: '提交失败' } })
    expect(wrapper.attributes('id')).toBeUndefined()
    expect(wrapper.classes()).toContain('form-error--block')
  })
})
