// @vitest-environment node

import { beforeEach, describe, expect, it, vi } from 'vitest'

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ api: requests, longRunningRequest: { timeout: 0 } }))

const operations = await import('./operations.js')

describe('operation status polling', () => {
  beforeEach(() => {
    requests.get.mockReset()
  })

  it.each([
    ['/panel-api/operations/op-panel', '/operations/op-panel'],
    ['/api/operations/op-legacy', '/operations/op-legacy']
  ])('routes %s through the based panel client as %s', async (statusURL, requestURL) => {
    requests.get.mockResolvedValueOnce({
      data: { operation_id: 'op', apply_status: 'applied' }
    })

    await operations.fetchOperationStatus(statusURL)

    expect(requests.get).toHaveBeenCalledWith(requestURL)
  })

	it('rejects an absolute status URL', async () => {
    await expect(operations.fetchOperationStatus('https://attacker.test/operations/op'))
      .rejects.toThrow('operation status URL is invalid')
    expect(requests.get).not.toHaveBeenCalled()
	})

  it('marks a completed applied no-op as terminal without drain rows', () => {
    const status = operations.normalizeOperationStatus({
      operation_id: 'op-noop', apply_status: 'applied', no_op: true, completed_at: '2026-07-18T12:00:00Z', agents: []
    })
    expect(status.ui_status).toBe('applied')
    expect(status.terminal).toBe(true)
  })
})
