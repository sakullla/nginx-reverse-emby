import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BulkActionBar from './BulkActionBar.vue'

function button(wrapper, action) {
  return wrapper.get(`[data-bulk-action="${action}"]`)
}

describe('BulkActionBar', () => {
  it('shows the selected count', () => {
    const wrapper = mount(BulkActionBar, { props: { count: 3 } })
    expect(wrapper.text()).toContain('已选 3 项')
    expect(wrapper.attributes('role')).toBe('toolbar')
  })

  it('emits enable, disable, delete and cancel', async () => {
    const wrapper = mount(BulkActionBar, { props: { count: 2 } })
    for (const action of ['enable', 'disable', 'delete', 'cancel']) {
      await button(wrapper, action).trigger('click')
      expect(wrapper.emitted(action)).toHaveLength(1)
    }
  })

  it('disables every action while busy and shows progress', async () => {
    const wrapper = mount(BulkActionBar, { props: { count: 2, busy: true, progress: { done: 1, total: 2 } } })
    for (const action of ['enable', 'disable', 'delete', 'cancel']) {
      expect(button(wrapper, action).attributes('disabled')).toBeDefined()
      await button(wrapper, action).trigger('click')
      expect(wrapper.emitted(action)).toBeUndefined()
    }
    expect(wrapper.text()).toContain('处理中 1/2')
  })

  it('disables state actions when nothing is selected but keeps cancel', () => {
    const wrapper = mount(BulkActionBar, { props: { count: 0 } })
    expect(button(wrapper, 'delete').attributes('disabled')).toBeDefined()
    expect(button(wrapper, 'cancel').attributes('disabled')).toBeUndefined()
  })

  it('emits cancel on Escape when idle', async () => {
    const wrapper = mount(BulkActionBar, { props: { count: 1 } })
    await wrapper.trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('cancel')).toHaveLength(1)
  })
})
