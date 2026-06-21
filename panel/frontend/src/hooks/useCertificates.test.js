import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'

// R3: isolate the smart-polling timer logic. A controllable query surface stands
// in for the real useQuery so the start/stop/dispose lifecycle can be exercised
// deterministically with fake timers, without a live network round-trip. The
// real-query integration path is covered separately by the useTrafficTrend
// harness pattern.
const queryData = ref([])
const refetch = vi.fn(() => Promise.resolve())

vi.mock('@tanstack/vue-query', () => ({
  useQuery: () => ({ data: queryData, refetch }),
  useMutation: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}))

vi.mock('../api', () => ({
  fetchCertificates: vi.fn(async () => []),
  createCertificate: vi.fn(),
  updateCertificate: vi.fn(),
  deleteCertificate: vi.fn(),
  issueCertificate: vi.fn(),
}))

vi.mock('../stores/messages', () => ({
  messageStore: { success: vi.fn(), error: vi.fn() },
}))

import { useCertificates } from './useCertificates.js'

const ISSUING_POLL_INTERVAL_MS = 4000

function withHook() {
  const scope = effectScope(true)
  scope.run(() => useCertificates(ref('agent-1')))
  return scope
}

describe('useCertificates smart polling (R3)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    refetch.mockClear()
    queryData.value = []
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('does not poll while no certificate is issuing', async () => {
    const scope = withHook()
    await nextTick()
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS * 3)
    expect(refetch).not.toHaveBeenCalled()
    scope.stop()
  })

  it('starts polling when an issuing certificate appears', async () => {
    const scope = withHook()
    await nextTick()
    queryData.value = [{ id: 1, status: 'issuing' }]
    await nextTick()
    // Interval armed, but the first tick hasn't elapsed yet.
    expect(refetch).not.toHaveBeenCalled()
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS)
    expect(refetch).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS)
    expect(refetch).toHaveBeenCalledTimes(2)
    scope.stop()
  })

  it('stops polling once every certificate leaves issuing', async () => {
    const scope = withHook()
    await nextTick()
    queryData.value = [{ id: 1, status: 'issuing' }]
    await nextTick()
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS)
    expect(refetch).toHaveBeenCalledTimes(1)
    queryData.value = [{ id: 1, status: 'active' }]
    await nextTick()
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS * 5)
    expect(refetch).toHaveBeenCalledTimes(1)
    scope.stop()
  })

  it('stops polling when its scope is disposed (no timer leak)', async () => {
    const scope = withHook()
    await nextTick()
    queryData.value = [{ id: 1, status: 'issuing' }]
    await nextTick()
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS)
    expect(refetch).toHaveBeenCalledTimes(1)
    scope.stop()
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS * 10)
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('does not stack extra intervals across data updates while still issuing', async () => {
    const scope = withHook()
    await nextTick()
    queryData.value = [{ id: 1, status: 'issuing' }]
    await nextTick()
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS)
    expect(refetch).toHaveBeenCalledTimes(1)
    // Still issuing — a refreshed list must not arm a second interval.
    queryData.value = [{ id: 1, status: 'issuing' }, { id: 2, status: 'issuing' }]
    await nextTick()
    vi.advanceTimersByTime(ISSUING_POLL_INTERVAL_MS)
    // Exactly one interval => exactly one refetch per tick.
    expect(refetch).toHaveBeenCalledTimes(2)
    scope.stop()
  })
})
