import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ListPagination from './ListPagination.vue'

describe('ListPagination', () => {
  it('renders nothing when total is 0', () => {
    const wrapper = mount(ListPagination, {
      props: { page: 1, pageSize: 20, total: 0 }
    })
    expect(wrapper.find('.list-pagination').exists()).toBe(false)
  })

  it('hides pagination controls when totalPages <= 1 and keeps concise total count', () => {
    const wrapper = mount(ListPagination, {
      props: { page: 1, pageSize: 20, total: 7 }
    })
    expect(wrapper.find('.list-pagination').exists()).toBe(true)
    expect(wrapper.find('.list-pagination__controls').exists()).toBe(false)
    expect(wrapper.findAll('button')).toHaveLength(0)
    expect(wrapper.get('.list-pagination__meta').text()).toBe('共 7 条')
  })

  it('shows controls and enforces first-page and last-page disable boundaries when totalPages > 1', async () => {
    const wrapper = mount(ListPagination, {
      props: { page: 1, pageSize: 20, total: 45 }
    })
    expect(wrapper.get('.list-pagination__meta').text()).toBe('共 45 条 · 第 1 / 3 页')
    const [prevBtn, nextBtn] = wrapper.findAll('button.list-pagination__btn')
    expect(prevBtn.attributes('disabled')).toBeDefined()
    expect(nextBtn.attributes('disabled')).toBeUndefined()

    await nextBtn.trigger('click')
    expect(wrapper.emitted('update:page')).toEqual([[2]])

    await wrapper.setProps({ page: 2 })
    expect(prevBtn.attributes('disabled')).toBeUndefined()
    expect(nextBtn.attributes('disabled')).toBeUndefined()

    await prevBtn.trigger('click')
    expect(wrapper.emitted('update:page')).toEqual([[2], [1]])

    await wrapper.setProps({ page: 3 })
    expect(prevBtn.attributes('disabled')).toBeUndefined()
    expect(nextBtn.attributes('disabled')).toBeDefined()

    await nextBtn.trigger('click')
    expect(wrapper.emitted('update:page')).toEqual([[2], [1]])
  })
})
