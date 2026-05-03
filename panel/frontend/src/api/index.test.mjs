import { beforeEach, describe, expect, it, vi } from 'vitest'

const runtimeVerifyToken = vi.fn(async () => true)
const devRuntimeVerifyToken = vi.fn(async () => true)
const runtimeFetchTrafficPolicy = vi.fn(async () => ({ direction: 'both' }))
const devRuntimeFetchTrafficPolicy = vi.fn(async () => ({ direction: 'both' }))
const runtimeUpdateTrafficPolicy = vi.fn(async () => ({ direction: 'rx' }))
const devRuntimeUpdateTrafficPolicy = vi.fn(async () => ({ direction: 'rx' }))
const runtimeFetchTrafficSummary = vi.fn(async () => ({ used_bytes: 0 }))
const devRuntimeFetchTrafficSummary = vi.fn(async () => ({ used_bytes: 0 }))
const runtimeFetchTrafficTrend = vi.fn(async () => [])
const devRuntimeFetchTrafficTrend = vi.fn(async () => [])
const runtimeCalibrateTraffic = vi.fn(async () => ({ used_bytes: 100 }))
const devRuntimeCalibrateTraffic = vi.fn(async () => ({ used_bytes: 100 }))
const runtimeCleanupTraffic = vi.fn(async () => ({ deleted_rows: 1 }))
const devRuntimeCleanupTraffic = vi.fn(async () => ({ deleted_rows: 1 }))

vi.mock('./runtime.js', () => ({
  verifyToken: runtimeVerifyToken,
  fetchTrafficPolicy: runtimeFetchTrafficPolicy,
  updateTrafficPolicy: runtimeUpdateTrafficPolicy,
  fetchTrafficSummary: runtimeFetchTrafficSummary,
  fetchTrafficTrend: runtimeFetchTrafficTrend,
  calibrateTraffic: runtimeCalibrateTraffic,
  cleanupTraffic: runtimeCleanupTraffic
}))

vi.mock('./devRuntime.js', () => ({
  verifyToken: devRuntimeVerifyToken,
  fetchTrafficPolicy: devRuntimeFetchTrafficPolicy,
  updateTrafficPolicy: devRuntimeUpdateTrafficPolicy,
  fetchTrafficSummary: devRuntimeFetchTrafficSummary,
  fetchTrafficTrend: devRuntimeFetchTrafficTrend,
  calibrateTraffic: devRuntimeCalibrateTraffic,
  cleanupTraffic: devRuntimeCleanupTraffic
}))

describe('api facade', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('delegates verifyToken to the selected runtime implementation', async () => {
    const api = await import('./index.js')

    const result = await api.verifyToken('panel-secret')

    expect(result).toBe(true)
    if (import.meta.env.DEV) {
      expect(devRuntimeVerifyToken).toHaveBeenCalledWith('panel-secret')
      expect(runtimeVerifyToken).not.toHaveBeenCalled()
      return
    }
    expect(runtimeVerifyToken).toHaveBeenCalledWith('panel-secret')
    expect(devRuntimeVerifyToken).not.toHaveBeenCalled()
  })

  it('does not expose the legacy token ref bridge', async () => {
    const api = await import('./index.js')

    expect('setApiTokenRef' in api).toBe(false)
  })

  it('exports traffic API helpers through the selected runtime implementation', async () => {
    const api = await import('./index.js')

    await api.fetchTrafficPolicy('edge/1')
    await api.updateTrafficPolicy('edge/1', { direction: 'rx' })
    await api.fetchTrafficSummary('edge/1')
    await api.fetchTrafficTrend('edge/1', { granularity: 'day' })
    await api.calibrateTraffic('edge/1', { used_bytes: 123 })
    await api.cleanupTraffic('edge/1')

    const selected = import.meta.env.DEV
      ? {
          fetchTrafficPolicy: devRuntimeFetchTrafficPolicy,
          updateTrafficPolicy: devRuntimeUpdateTrafficPolicy,
          fetchTrafficSummary: devRuntimeFetchTrafficSummary,
          fetchTrafficTrend: devRuntimeFetchTrafficTrend,
          calibrateTraffic: devRuntimeCalibrateTraffic,
          cleanupTraffic: devRuntimeCleanupTraffic
        }
      : {
          fetchTrafficPolicy: runtimeFetchTrafficPolicy,
          updateTrafficPolicy: runtimeUpdateTrafficPolicy,
          fetchTrafficSummary: runtimeFetchTrafficSummary,
          fetchTrafficTrend: runtimeFetchTrafficTrend,
          calibrateTraffic: runtimeCalibrateTraffic,
          cleanupTraffic: runtimeCleanupTraffic
        }

    expect(selected.fetchTrafficPolicy).toHaveBeenCalledWith('edge/1')
    expect(selected.updateTrafficPolicy).toHaveBeenCalledWith('edge/1', { direction: 'rx' })
    expect(selected.fetchTrafficSummary).toHaveBeenCalledWith('edge/1')
    expect(selected.fetchTrafficTrend).toHaveBeenCalledWith('edge/1', { granularity: 'day' })
    expect(selected.calibrateTraffic).toHaveBeenCalledWith('edge/1', { used_bytes: 123 })
    expect(selected.cleanupTraffic).toHaveBeenCalledWith('edge/1')
  })
})

describe('runtime traffic APIs', () => {
  it('calls traffic backend routes with encoded agent ids and query params', async () => {
    vi.resetModules()
    const { api: runtimeClient } = await vi.importActual('./client.js')
    const calls = []
    runtimeClient.defaults.adapter = async (config) => {
      calls.push({
        method: config.method,
        url: config.url,
        data: config.data
      })
      if (config.url.includes('traffic-policy') && config.method === 'get') {
        return { data: { policy: { direction: 'both' } }, status: 200, statusText: 'OK', headers: {}, config }
      }
      if (config.url.includes('traffic-policy') && config.method === 'patch') {
        return { data: { policy: { direction: 'rx' } }, status: 200, statusText: 'OK', headers: {}, config }
      }
      if (config.url.includes('traffic-summary')) {
        return { data: { summary: { used_bytes: 10 } }, status: 200, statusText: 'OK', headers: {}, config }
      }
      if (config.url.includes('traffic-trend')) {
        return { data: { points: [{ bucket_start: '2026-05-03T00:00:00Z' }] }, status: 200, statusText: 'OK', headers: {}, config }
      }
      if (config.url.includes('traffic-calibration')) {
        return { data: { summary: { used_bytes: 123 } }, status: 200, statusText: 'OK', headers: {}, config }
      }
      return { data: { result: { deleted_rows: 1 } }, status: 200, statusText: 'OK', headers: {}, config }
    }

    const runtime = await vi.importActual('./runtime.js')

    await runtime.fetchTrafficPolicy('edge/1')
    await runtime.updateTrafficPolicy('edge/1', { direction: 'rx' })
    await runtime.fetchTrafficSummary('edge/1')
    await runtime.fetchTrafficTrend('edge/1', {
      granularity: 'day',
      from: '2026-05-01T00:00:00Z',
      to: '',
      scope_type: 'http_rule',
      scope_id: '7'
    })
    await runtime.calibrateTraffic('edge/1', { used_bytes: 123 })
    await runtime.cleanupTraffic('edge/1')

    expect(calls.map((call) => `${call.method} ${call.url}`)).toEqual([
      'get /agents/edge%2F1/traffic-policy',
      'patch /agents/edge%2F1/traffic-policy',
      'get /agents/edge%2F1/traffic-summary',
      'get /agents/edge%2F1/traffic-trend?granularity=day&from=2026-05-01T00%3A00%3A00Z&scope_type=http_rule&scope_id=7',
      'post /agents/edge%2F1/traffic-calibration',
      'post /agents/edge%2F1/traffic-cleanup'
    ])
    expect(JSON.parse(calls[1].data)).toEqual({ direction: 'rx' })
    expect(JSON.parse(calls[4].data)).toEqual({ used_bytes: 123 })
    expect(calls[5].data == null || calls[5].data === '').toBe(true)
  })
})
