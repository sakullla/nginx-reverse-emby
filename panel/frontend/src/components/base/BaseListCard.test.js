import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BaseListCard from './BaseListCard.vue'

function mountCard(props = {}, slots = {}) {
  return mount(BaseListCard, {
    props,
    slots: {
      default: '<span class="body">body</span>',
      ...slots,
    },
  })
}

describe('BaseListCard', () => {
  it('sets data-status when status is provided (badge contract; no left strip)', () => {
    const wrapper = mountCard({ status: 'success' })
    const root = wrapper.find('.base-list-card')
    expect(root.attributes('data-status')).toBe('success')
  })

  it('omits data-status attribute when status is null', () => {
    const wrapper = mountCard({ status: null })
    const root = wrapper.find('.base-list-card')
    // Vue may omit null attrs; ensure not a status tone value
    const attr = root.attributes('data-status')
    expect(attr === undefined || attr === null || attr === '').toBe(true)
  })

  it.each(['success', 'warning', 'danger', 'neutral'])(
    'accepts status=%s for data-status contract',
    (status) => {
      const wrapper = mountCard({ status })
      expect(wrapper.find('.base-list-card').attributes('data-status')).toBe(status)
    },
  )

  it('applies disabled class when disabled', () => {
    const wrapper = mountCard({ disabled: true })
    expect(wrapper.find('.base-list-card--disabled').exists()).toBe(true)
  })

  it('renders title when provided', () => {
    const wrapper = mountCard({ title: 'https://example.com' })
    expect(wrapper.find('.base-list-card__title').text()).toBe('https://example.com')
  })
})
