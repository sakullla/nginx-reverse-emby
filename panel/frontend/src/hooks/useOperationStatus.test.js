import { VueQueryPlugin } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ events: vi.fn() }))
vi.mock('../api/operations', async (importOriginal) => ({
  ...await importOriginal(),
  fetchRevisionEvents: api.events
}))

import { useOperationStatus } from './useOperationStatus'
import { recordAcceptedOperation, resetOperations, useOperationsStore } from '../stores/operations'

describe('useOperationStatus', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetOperations()
    api.events.mockReset()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('refreshes status after an event notification and stops polling at terminal state', async () => {
    recordAcceptedOperation({ operation_id: 'op-hook', status_url: '/panel-api/operations/op-hook', apply_status: 'pending' })
    const store = useOperationsStore()
    const refresh = vi.spyOn(store, 'refresh')
      .mockResolvedValueOnce(recordAcceptedOperation({ operation_id: 'op-hook', apply_status: 'applied', status_url: '/panel-api/operations/op-hook' }))
    let exposed
    const wrapper = mount(defineComponent({
      setup() {
        exposed = useOperationStatus(ref('op-hook'), { pollInterval: 1000 })
        return () => null
      }
    }), { global: { plugins: [VueQueryPlugin] } })

    await exposed.notifyEvent({ operation_id: 'op-hook' })
    await nextTick()
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(exposed.operation.value.ui_status).toBe('applied')
    wrapper.unmount()
  })

  it('falls back to the status URL when revision events are unavailable', async () => {
    recordAcceptedOperation({ operation_id: 'op-lost', status_url: '/panel-api/operations/op-lost', apply_status: 'pending' })
    api.events.mockRejectedValueOnce(new Error('event cursor lost'))
    const store = useOperationsStore()
    const refresh = vi.spyOn(store, 'refresh')
      .mockResolvedValueOnce(recordAcceptedOperation({ operation_id: 'op-lost', apply_status: 'applied', status_url: '/panel-api/operations/op-lost' }))
    let exposed
    const wrapper = mount(defineComponent({
      setup() {
        exposed = useOperationStatus(ref('op-lost'), { pollInterval: 1000 })
        return () => null
      }
    }))

    await exposed.recover()
    await nextTick()

    expect(api.events).toHaveBeenCalledWith(0, { operationId: 'op-lost', limit: 100 })
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(exposed.operation.value.ui_status).toBe('applied')
    wrapper.unmount()
  })
})
