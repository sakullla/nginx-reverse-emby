import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { computed, nextTick, ref } from 'vue'
import TrafficTrendModal from './TrafficTrendModal.vue'

const trafficTrendCalls = []

vi.mock('../../hooks/useTraffic.js', () => ({
  useTrafficTrend: vi.fn((agentId, params) => {
    trafficTrendCalls.push({ agentId, params })
    return {
      data: ref([]),
      isLoading: ref(false)
    }
  })
}))

vi.mock('./TrafficTrendChart.vue', () => ({
  default: {
    name: 'TrafficTrendChart',
    props: ['points', 'prevPoints'],
    template: '<div class="chart-stub"></div>'
  }
}))

vi.mock('../base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: ['modelValue', 'title', 'size'],
    emits: ['update:modelValue'],
    template: '<div v-if="modelValue" class="modal-stub"><slot /></div>'
  }
}))

describe('TrafficTrendModal', () => {
  it('does not enable previous-period traffic query without a complete range', async () => {
    trafficTrendCalls.length = 0
    const wrapper = mount(TrafficTrendModal, {
      props: {
        visible: true,
        agentId: 'edge-1',
        scopeType: 'http_rule',
        scopeId: '7',
        scopeLabel: 'Rule 7'
      }
    })

    const previousCall = trafficTrendCalls[1]
    expect(computed(() => previousCall.agentId.value).value).toBeNull()

    await wrapper.find('.traffic-trend-modal__compare input').setValue(true)
    await nextTick()
    expect(computed(() => previousCall.agentId.value).value).toBeNull()

    const inputs = wrapper.findAll('.traffic-trend-modal__date-input')
    await inputs[0].setValue('2026-05-04')
    await inputs[1].setValue('2026-05-10')
    await nextTick()

    expect(computed(() => previousCall.agentId.value).value).toBe('edge-1')
    expect(computed(() => previousCall.params.value.from).value).toBe('2026-04-27T00:00:00.000Z')
  })
})
