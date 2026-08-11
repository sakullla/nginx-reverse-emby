import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SecretWriteOnlyField from './SecretWriteOnlyField.vue'

describe('SecretWriteOnlyField', () => {
  it('shows preserve state and exposes clear only when the broker allows it', async () => {
    const wrapper = mount(SecretWriteOnlyField, { props: { present: true, clearable: true } })
    expect(wrapper.text()).toContain('留空会保留现值')
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('clear')).toHaveLength(1)

    await wrapper.setProps({ required: true, clearable: false })
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.get('input').attributes('required')).toBeDefined()
  })
})
