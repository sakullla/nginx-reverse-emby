// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'

const api = vi.hoisted(() => ({ fetch: vi.fn() }))
vi.mock('../api/pki', async importOriginal => {
  const original = await importOriginal()
  return { ...original, fetchPkiOperationStatus: api.fetch }
})

const tracker = await import('./usePkiOperations.js')

describe('internal PKI operation recovery', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    api.fetch.mockReset()
    tracker.resetPkiOperationMemory(localStorage)
  })

  it('persists only safe operation metadata and never persists result secrets', () => {
    tracker.recordPkiOperation({
      id: 'op-export',
      state: 'succeeded',
      kind: 'protected_export',
      result: { archive: 'base64-secret', passphrase: 'must-not-persist' }
    }, localStorage)

    const raw = localStorage.getItem(tracker.PKI_OPERATION_STORAGE_KEY)
    expect(raw).toContain('op-export')
    expect(raw).not.toContain('base64-secret')
    expect(raw).not.toContain('must-not-persist')
  })

  it('restores an accepted operation and polls the dedicated status endpoint', async () => {
    localStorage.setItem(tracker.PKI_OPERATION_STORAGE_KEY, JSON.stringify([{
      id: 'op-running',
      status_url: '/panel-api/pki/operations/op-running',
      state: 'accepted'
    }]))
    api.fetch.mockResolvedValue({ id: 'op-running', state: 'succeeded', updated_at: '2026-08-03T01:00:00Z' })

    const scope = effectScope(true)
    let hook
    scope.run(() => { hook = tracker.usePkiOperations({ pollInterval: 1000, refreshOnRestore: false, storage: localStorage }) })
    await nextTick()
    vi.advanceTimersByTime(1000)
    await vi.runOnlyPendingTimersAsync()

    expect(api.fetch).toHaveBeenCalledWith('/panel-api/pki/operations/op-running')
    expect(hook.operations.value[0]).toMatchObject({ state: 'succeeded', terminal: true })
    scope.stop()
  })

  it.each([503, 409, 404])('keeps operation identity recoverable when status returns %s', async status => {
    tracker.recordPkiOperation({ id: `op-${status}`, state: 'running' }, localStorage)
    const error = new Error(`status ${status}`)
    error.status = status
    api.fetch.mockRejectedValue(error)

    const scope = effectScope(true)
    let hook
    scope.run(() => { hook = tracker.usePkiOperations({ pollInterval: -1, refreshOnRestore: false, storage: localStorage }) })
    await hook.refresh(`op-${status}`)

    expect(hook.operations.value[0].id).toBe(`op-${status}`)
    expect(hook.errors.value[`op-${status}`]).toMatchObject({ status, recoverable: true })
    scope.stop()
  })
})
