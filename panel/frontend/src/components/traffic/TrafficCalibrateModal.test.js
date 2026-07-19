import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import TrafficCalibrateModal from './TrafficCalibrateModal.vue'

vi.mock('../base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: ['modelValue', 'title', 'size'],
    emits: ['update:modelValue'],
    template: '<div v-if="modelValue" class="modal-stub"><div class="modal-title">{{ title }}</div><slot /></div>'
  }
}))

describe('TrafficCalibrateModal', () => {
  it('renders when visible is true', async () => {
    const wrapper = mount(TrafficCalibrateModal, {
      props: {
        visible: true,
        agentId: 'edge-1',
        currentUsedBytes: 1073741824,
        cycleStart: '2026-05-01T00:00:00Z',
        cycleEnd: '2026-06-01T00:00:00Z'
      }
    })
    expect(wrapper.find('.traffic-calibrate-modal').exists()).toBe(true)
    expect(wrapper.text()).toContain('校准当前周期已用流量')
    expect(wrapper.text()).toContain('2026/5/1')
  })

  it('emits confirm with parsed bytes on submit', async () => {
    const wrapper = mount(TrafficCalibrateModal, {
      props: {
        visible: true,
        agentId: 'edge-1',
        currentUsedBytes: 1073741824,
        cycleStart: '2026-05-01T00:00:00Z',
        cycleEnd: '2026-06-01T00:00:00Z'
      }
    })
    const input = wrapper.find('.traffic-calibrate-modal__input')
    await input.setValue('1.5')
    const select = wrapper.find('.traffic-calibrate-modal__unit')
    await select.setValue('GiB')
    await wrapper.find('.traffic-calibrate-modal__confirm').trigger('click')
    await nextTick()
    expect(wrapper.emitted('confirm')).toHaveLength(1)
    expect(wrapper.emitted('confirm')[0]).toEqual([1610612736])
  })

  it('treats bare numeric calibration values as bytes', async () => {
    const wrapper = mount(TrafficCalibrateModal, {
      props: {
        visible: true,
        agentId: 'edge-1',
        currentUsedBytes: 0,
        cycleStart: '2026-05-01T00:00:00Z',
        cycleEnd: '2026-06-01T00:00:00Z'
      }
    })
    const input = wrapper.find('.traffic-calibrate-modal__input')
    await input.setValue('1610612736')
    await wrapper.find('.traffic-calibrate-modal__confirm').trigger('click')
    await nextTick()
    expect(wrapper.emitted('confirm')).toHaveLength(1)
    expect(wrapper.emitted('confirm')[0]).toEqual([1610612736])
  })

  it('emits update:visible false on cancel', async () => {
    const wrapper = mount(TrafficCalibrateModal, {
      props: {
        visible: true,
        agentId: 'edge-1',
        currentUsedBytes: 0,
        cycleStart: '',
        cycleEnd: ''
      }
    })
    await wrapper.find('.traffic-calibrate-modal__cancel').trigger('click')
    await nextTick()
    expect(wrapper.emitted('update:visible')).toHaveLength(1)
    expect(wrapper.emitted('update:visible')[0]).toEqual([false])
  })
})
