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

  it('uses production revision events to refresh through drained terminal state', async () => {
    recordAcceptedOperation({ operation_id: 'op-hook', status_url: '/panel-api/operations/op-hook', apply_status: 'pending' })
    api.events.mockResolvedValueOnce({
      events: [{ id: 1, operation_id: 'op-hook', event_type: 'generation_drained' }],
      next_cursor: 1,
      has_more: false
    })
    const store = useOperationsStore()
    const refresh = vi.spyOn(store, 'refresh')
      .mockImplementationOnce(async () => recordAcceptedOperation({
        operation_id: 'op-hook',
        apply_status: 'applied',
        status_url: '/panel-api/operations/op-hook',
        agents: [{ agent_id: 'edge-1', apply_status: 'applied', drain_status: 'drained' }]
      }))
    let exposed
    const wrapper = mount(defineComponent({
      setup() {
        exposed = useOperationStatus(ref('op-hook'), { pollInterval: 1000 })
        return () => null
      }
    }), { global: { plugins: [VueQueryPlugin] } })

    await exposed.recover()
    await nextTick()
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(exposed.operation.value.ui_status).toBe('drained')
    expect(exposed.eventStatus.value).toBe('connected')
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
    expect(exposed.eventStatus.value).toBe('disconnected')
    wrapper.unmount()
  })

  it('reconnects the event cursor after using the status fallback', async () => {
    recordAcceptedOperation({ operation_id: 'op-reconnect', status_url: '/panel-api/operations/op-reconnect', apply_status: 'pending' })
    api.events
      .mockRejectedValueOnce(new Error('disconnected'))
      .mockResolvedValueOnce({
        events: [{ id: 8, operation_id: 'op-reconnect', event_type: 'revision_applied' }],
        next_cursor: 8,
        has_more: false
      })
    const store = useOperationsStore()
    vi.spyOn(store, 'refresh')
      .mockImplementationOnce(async () => store.get('op-reconnect'))
      .mockImplementationOnce(async () => recordAcceptedOperation({
        operation_id: 'op-reconnect',
        status_url: '/panel-api/operations/op-reconnect',
        apply_status: 'applied',
        agents: [{ agent_id: 'edge-1', apply_status: 'applied', drain_status: 'drained' }]
      }))
    let exposed
    const wrapper = mount(defineComponent({
      setup() {
        exposed = useOperationStatus(ref('op-reconnect'), { pollInterval: 1000 })
        return () => null
      }
    }))

    await exposed.recover()
    expect(exposed.eventStatus.value).toBe('disconnected')
    await exposed.recover()
    expect(exposed.eventStatus.value).toBe('connected')
    expect(exposed.operation.value.ui_status).toBe('drained')
    wrapper.unmount()
  })
})
