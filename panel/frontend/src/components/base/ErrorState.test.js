import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ErrorState from './ErrorState.vue'

describe('ErrorState', () => {
  it('announces the failure as an alert with title and message', () => {
    const wrapper = mount(ErrorState, { props: { message: '网络连接超时' } })
    expect(wrapper.attributes('role')).toBe('alert')
    expect(wrapper.text()).toContain('加载失败')
    expect(wrapper.text()).toContain('网络连接超时')
  })

  it('emits retry when the retry button is clicked', async () => {
    const wrapper = mount(ErrorState, { props: { message: 'boom' } })
    const button = wrapper.get('button')
    expect(button.text()).toContain('重试')
    await button.trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })

  it('disables the retry button while retrying', async () => {
    const wrapper = mount(ErrorState, { props: { message: 'boom', retrying: true } })
    const button = wrapper.get('button')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.attributes('aria-busy')).toBe('true')
    await button.trigger('click')
    expect(wrapper.emitted('retry')).toBeUndefined()
  })

  it('hides the retry button when showRetry is false', () => {
    const wrapper = mount(ErrorState, { props: { title: '无法加载节点', showRetry: false } })
    expect(wrapper.text()).toContain('无法加载节点')
    expect(wrapper.find('button').exists()).toBe(false)
  })
})
