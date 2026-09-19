import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ events: vi.fn() }))
vi.mock('../api/operations', async (importOriginal) => ({
  ...await importOriginal(),
  fetchRevisionEvents: api.events
}))

import { useOperationsStatus } from './useOperationsStatus'
import { recordAcceptedOperation, resetOperations, useOperationsStore } from '../stores/operations'

function track(id) {
  recordAcceptedOperation({
    operation_id: id,
    status_url: `/panel-api/operations/${id}`,
    apply_status: 'pending'
  })
}

describe('useOperationsStatus', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetOperations()
    api.events.mockReset()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('polls one global event stream and refreshes only operations with events', async () => {
    track('op-a')
    track('op-b')
    api.events.mockResolvedValue({
      events: [{ id: 8, operation_id: 'op-b', event_type: 'revision_applied' }],
      next_cursor: 8,
      has_more: false
    })
    const store = useOperationsStore()
    const refresh = vi.spyOn(store, 'refresh').mockImplementation(async (id) => store.get(id))
    let scheduler
    const wrapper = mount(defineComponent({
      setup() {
        scheduler = useOperationsStatus({ pollInterval: -1 })
        return () => null
      }
    }))

    await scheduler.recover()

    expect(api.events).toHaveBeenCalledTimes(1)
    expect(api.events).toHaveBeenCalledWith(0, { limit: 500 })
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(refresh).toHaveBeenCalledWith('op-b')
    expect(scheduler.eventStatus.value).toBe('connected')
    wrapper.unmount()
  })

  it('bounds historical catch-up pages and uses one status fallback pass', async () => {
    track('op-a')
    track('op-b')
    api.events
      .mockResolvedValueOnce({ events: [], next_cursor: 500, has_more: true })
      .mockResolvedValueOnce({ events: [], next_cursor: 1000, has_more: true })
    const store = useOperationsStore()
    const refresh = vi.spyOn(store, 'refresh').mockImplementation(async (id) => store.get(id))
    let scheduler
    const wrapper = mount(defineComponent({
      setup() {
        scheduler = useOperationsStatus({ pollInterval: -1, maxEventPages: 2, statusConcurrency: 1 })
        return () => null
      }
    }))

    await scheduler.recover()
    await nextTick()

    expect(api.events.mock.calls).toEqual([
      [0, { limit: 500 }],
      [500, { limit: 500 }]
    ])
    expect(refresh.mock.calls.map(([id]) => id).sort()).toEqual(['op-a', 'op-b'])
    wrapper.unmount()
  })

  it('falls back once for all tracked operations when the event stream fails', async () => {
    track('op-a')
    track('op-b')
    api.events.mockRejectedValue(new Error('event stream unavailable'))
    const store = useOperationsStore()
    const refresh = vi.spyOn(store, 'refresh').mockImplementation(async (id) => store.get(id))
    let scheduler
    const wrapper = mount(defineComponent({
      setup() {
        scheduler = useOperationsStatus({ pollInterval: -1, statusConcurrency: 2 })
        return () => null
      }
    }))

    await scheduler.recover()

    expect(api.events).toHaveBeenCalledTimes(1)
    expect(refresh.mock.calls.map(([id]) => id).sort()).toEqual(['op-a', 'op-b'])
    expect(scheduler.eventStatus.value).toBe('disconnected')
    wrapper.unmount()
  })
})
