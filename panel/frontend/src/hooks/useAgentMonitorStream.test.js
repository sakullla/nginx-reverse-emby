import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { computed, defineComponent, nextTick, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setAuthToken, clearAuthToken } from '../api/authState.js'
import { useAgentMonitorStream, AGENT_MONITOR_QUERY_KEY } from './useAgentMonitorStream.js'
import * as api from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    consumeAgentMonitorStream: vi.fn()
  }
})

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false }
    }
  })
}

function mountHarness(queryClient, options = {}) {
  let exposed
  const Harness = defineComponent({
    setup() {
      exposed = useAgentMonitorStream(options)
      return () => null
    }
  })
  const wrapper = mount(Harness, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]]
    }
  })
  return { wrapper, exposed }
}

describe('useAgentMonitorStream', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setAuthToken('panel-secret')
    api.consumeAgentMonitorStream.mockReset()
  })

  afterEach(() => {
    clearAuthToken()
    vi.useRealTimers()
  })

  it('merges snapshot and update messages into monitor and agents caches', async () => {
    const queryClient = createQueryClient()
    queryClient.setQueryData(['agents'], [{ id: 'edge-1', name: 'Old Edge', status: 'offline' }])
    api.consumeAgentMonitorStream.mockImplementation(async ({ onMessage }) => {
      onMessage({ type: 'snapshot', payload: { agents: [{ id: 'edge-1', name: 'Edge 1', status: 'online' }] } })
      onMessage({ type: 'update', payload: { agent: { id: 'edge-1', status: 'offline', last_seen_ip: '203.0.113.9' } } })
    })

    const { wrapper, exposed } = mountHarness(queryClient, { reconnectDelay: -1 })
    await nextTick()
    await vi.dynamicImportSettled()

    expect(exposed.data.value).toEqual([{ id: 'edge-1', name: 'Edge 1', status: 'offline', last_seen_ip: '203.0.113.9' }])
    expect(queryClient.getQueryData(AGENT_MONITOR_QUERY_KEY)).toEqual(exposed.data.value)
    expect(queryClient.getQueryData(['agents'])[0]).toMatchObject({
      id: 'edge-1',
      name: 'Edge 1',
      status: 'offline',
      last_seen_ip: '203.0.113.9',
      monitor: expect.objectContaining({ id: 'edge-1', status: 'offline' })
    })
    expect(exposed.status.value).toBe('disconnected')
    wrapper.unmount()
  })

  it('aborts the stream when disabled', async () => {
    const enabled = computed(() => true)
    const queryClient = createQueryClient()
    const signals = []
    let resolveStream
    api.consumeAgentMonitorStream.mockImplementation(({ signal }) => {
      signals.push(signal)
      return new Promise((resolve) => {
        resolveStream = resolve
      })
    })

    const { wrapper } = mountHarness(queryClient, { enabled, reconnectDelay: -1 })
    await nextTick()
    expect(signals).toHaveLength(1)
    expect(signals[0].aborted).toBe(false)

    wrapper.unmount()
    expect(signals[0].aborted).toBe(true)
    resolveStream()
  })

  it('starts and stops the stream when enabled changes dynamically', async () => {
    const enabled = ref(true)
    const queryClient = createQueryClient()
    const signals = []
    api.consumeAgentMonitorStream.mockImplementation(({ signal }) => {
      signals.push(signal)
      return new Promise(() => {})
    })

    const { wrapper } = mountHarness(queryClient, { enabled, reconnectDelay: -1 })
    await nextTick()
    expect(signals).toHaveLength(1)
    expect(signals[0].aborted).toBe(false)

    enabled.value = false
    await nextTick()
    await Promise.resolve()
    expect(signals[0].aborted).toBe(true)

    enabled.value = true
    await nextTick()
    await Promise.resolve()
    expect(signals).toHaveLength(2)
    expect(signals[1].aborted).toBe(false)

    wrapper.unmount()
  })

  it('schedules a reconnect after stream errors', async () => {
    const queryClient = createQueryClient()
    let rejectFirst
    api.consumeAgentMonitorStream.mockImplementationOnce(() => new Promise((_, reject) => {
      rejectFirst = reject
    }))
    api.consumeAgentMonitorStream.mockImplementation(() => new Promise(() => {}))

    const { wrapper, exposed } = mountHarness(queryClient, { reconnectDelay: 25 })
    await nextTick()
    rejectFirst(new Error('network down'))
    await Promise.resolve()
    await Promise.resolve()
    expect(exposed.status.value).toBe('error')
    expect(api.consumeAgentMonitorStream).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(25)
    expect(api.consumeAgentMonitorStream).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('preserves monitor data while reconnecting after the backend closes the stream', async () => {
    const queryClient = createQueryClient()
    let resolveFirst
    api.consumeAgentMonitorStream.mockImplementationOnce(async ({ onMessage }) => {
      onMessage({ type: 'snapshot', payload: { agents: [{ id: 'edge-1', status: 'online' }] } })
      return new Promise((resolve) => { resolveFirst = resolve })
    })
    api.consumeAgentMonitorStream.mockImplementation(() => new Promise(() => {}))

    const { wrapper, exposed } = mountHarness(queryClient, { reconnectDelay: 25 })
    await nextTick()
    expect(exposed.data.value).toEqual([{ id: 'edge-1', status: 'online' }])

    resolveFirst()
    await Promise.resolve()
    await Promise.resolve()
    expect(exposed.status.value).toBe('disconnected')
    expect(exposed.data.value).toEqual([{ id: 'edge-1', status: 'online' }])

    await vi.advanceTimersByTimeAsync(25)
    expect(api.consumeAgentMonitorStream).toHaveBeenCalledTimes(2)
    expect(exposed.data.value).toEqual([{ id: 'edge-1', status: 'online' }])

    wrapper.unmount()
  })
})
