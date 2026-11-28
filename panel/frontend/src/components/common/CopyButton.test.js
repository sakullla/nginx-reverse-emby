import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import CopyButton from './CopyButton.vue'
import * as clipboard from '../../utils/clipboard.js'
import { messageStore } from '../../stores/messages.js'

describe('CopyButton', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    messageStore.clearAll()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
    messageStore.clearAll()
  })

  it.each(['', '   ', '-', '—', null, undefined])('hides automatically for empty or placeholder value %s', (value) => {
    const wrapper = mount(CopyButton, { props: { value } })
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it('copies value, shows temporary copied state, triggers success toast, and stops click propagation', async () => {
    const copySpy = vi.spyOn(clipboard, 'copyText').mockResolvedValue({ ok: true })
    const successSpy = vi.spyOn(messageStore, 'success')
    const parentClick = vi.fn()

    const Host = defineComponent({
      components: { CopyButton },
      setup() {
        return { parentClick }
      },
      template: '<div class="host" @click="parentClick"><CopyButton value="https://emby.example.com" label="复制入口地址" success-message="已复制入口地址" /></div>'
    })

    const wrapper = mount(Host)
    const button = wrapper.get('button.copy-button')
    expect(button.attributes('aria-label')).toBe('复制入口地址')

    await button.trigger('click')
    await flushPromises()

    expect(parentClick).not.toHaveBeenCalled()
    expect(copySpy).toHaveBeenCalledWith('https://emby.example.com')
    expect(successSpy).toHaveBeenCalledWith('已复制入口地址')
    expect(button.classes()).toContain('copy-button--copied')
    expect(button.attributes('aria-label')).toBe('已复制')

    vi.advanceTimersByTime(1700)
    await flushPromises()
    expect(button.classes()).not.toContain('copy-button--copied')
    expect(button.attributes('aria-label')).toBe('复制入口地址')
  })

  it('shows error toast and emits error when copy fails', async () => {
    vi.spyOn(clipboard, 'copyText').mockResolvedValue({ ok: false, error: '当前环境不支持复制' })
    const errorSpy = vi.spyOn(messageStore, 'error')

    const wrapper = mount(CopyButton, {
      props: { value: '10.0.0.2:8096' }
    })

    await wrapper.get('button.copy-button').trigger('click')
    await flushPromises()

    expect(errorSpy).toHaveBeenCalledWith('当前环境不支持复制', '复制失败')
    expect(wrapper.emitted('error')?.[0]).toEqual(['当前环境不支持复制'])
    expect(wrapper.get('button.copy-button').classes()).not.toContain('copy-button--copied')
  })
})
