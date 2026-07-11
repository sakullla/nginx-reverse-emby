import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ListPagination from './ListPagination.vue'

describe('ListPagination', () => {
  it('renders total and page meta', () => {
    const wrapper = mount(ListPagination, {
      props: { page: 2, pageSize: 20, total: 45 }
    })
    expect(wrapper.text()).toContain('共 45 条')
    expect(wrapper.text()).toContain('第 2 / 3 页')
  })

  it('emits previous/next page updates and clamps bounds', async () => {
    const wrapper = mount(ListPagination, {
      props: { page: 2, pageSize: 10, total: 25 }
    })
    const buttons = wrapper.findAll('button')
    await buttons[0].trigger('click')
    expect(wrapper.emitted('update:page')?.[0]).toEqual([1])
    await buttons[1].trigger('click')
    expect(wrapper.emitted('update:page')?.[1]).toEqual([3])
  })

  it('hides when total is 0', () => {
    const wrapper = mount(ListPagination, {
      props: { page: 1, pageSize: 20, total: 0 }
    })
    expect(wrapper.find('.list-pagination').exists()).toBe(false)
  })
})
