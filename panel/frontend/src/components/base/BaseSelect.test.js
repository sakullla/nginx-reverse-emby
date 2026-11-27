import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BaseSelect from './BaseSelect.vue'

const OPTIONS = [
  { value: 'last_seen_at', label: '最后活跃' },
  { value: 'name', label: '名称' },
  { value: 'http_rules_count', label: 'HTTP 规则' }
]

let wrapper

function mountSelect(props = {}) {
  wrapper = mount(BaseSelect, {
    attachTo: document.body,
    props: { modelValue: 'last_seen_at', options: OPTIONS, ariaLabel: '排序字段', ...props }
  })
  return wrapper
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

describe('BaseSelect', () => {
  it('shows the selected label and picks an option with the mouse', async () => {
    const w = mountSelect()
    const trigger = w.get('button[aria-label="排序字段"]')
    expect(trigger.text()).toBe('最后活跃')
    expect(trigger.attributes('aria-expanded')).toBe('false')

    await trigger.trigger('click')
    expect(trigger.attributes('aria-expanded')).toBe('true')
    const selected = w.findAll('[role="option"]').filter((option) => option.attributes('aria-selected') === 'true')
    expect(selected.map((option) => option.text())).toEqual(['最后活跃'])

    await w.findAll('[role="option"]')[1].trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['name']])
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('supports keyboard selection starting from the current value', async () => {
    const w = mountSelect({ modelValue: 'name' })
    const trigger = w.get('button[aria-label="排序字段"]')

    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await nextTick()
    const list = w.get('[role="listbox"]')
    expect(document.activeElement).toBe(list.element)
    expect(list.attributes('aria-activedescendant')).toBe(w.findAll('[role="option"]')[1].attributes('id'))

    await list.trigger('keydown', { key: 'ArrowDown' })
    await list.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')).toEqual([['http_rules_count']])
    expect(document.activeElement).toBe(trigger.element)
  })

  it('closes without emitting on Escape or an outside click', async () => {
    const w = mountSelect()
    const trigger = w.get('button[aria-label="排序字段"]')

    await trigger.trigger('click')
    await w.get('[role="listbox"]').trigger('keydown', { key: 'Escape' })
    expect(w.find('[role="listbox"]').exists()).toBe(false)
    expect(document.activeElement).toBe(trigger.element)

    await trigger.trigger('click')
    document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()
    expect(w.find('[role="listbox"]').exists()).toBe(false)
    expect(w.emitted('update:modelValue')).toBeUndefined()
  })
})
