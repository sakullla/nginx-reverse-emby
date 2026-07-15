import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ fetch: vi.fn(), retry: vi.fn(), rollback: vi.fn() }))
vi.mock('../api/operations', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    fetchOperationStatus: api.fetch,
    retryRevision: api.retry,
    rollbackRevision: api.rollback
  }
})

const storeModule = await import('./operations.js')

describe('operation store', () => {
  beforeEach(() => {
    localStorage.clear()
    storeModule.resetOperations()
    api.fetch.mockReset()
    api.retry.mockReset()
    api.rollback.mockReset()
  })

  it('persists accepted operations and restores them after reload semantics', () => {
    storeModule.recordAcceptedOperation({
      operation_id: 'op-1', status_url: '/panel-api/operations/op-1', apply_status: 'pending'
    })
    storeModule.recordAcceptedOperation({
      operation_id: 'op-2', status_url: '/panel-api/operations/op-2', apply_status: 'pending'
    })
    const persisted = JSON.parse(localStorage.getItem('nre.operations.v1'))
    expect(persisted.map((operation) => operation.operation_id)).toEqual(['op-2', 'op-1'])
    expect(persisted[0]).toMatchObject({ operation_id: 'op-2', ui_status: 'pending' })
    storeModule.resetOperations()
    localStorage.setItem('nre.operations.v1', JSON.stringify(persisted))
    expect(storeModule.restoreOperations().map((operation) => operation.operation_id)).toEqual(['op-2', 'op-1'])
  })

  it('recovers from stream loss by querying the persisted status URL', async () => {
    storeModule.recordAcceptedOperation({
      operation_id: 'op-2', status_url: '/panel-api/operations/op-2', apply_status: 'pending'
    })
    api.fetch.mockResolvedValueOnce({ operation_id: 'op-2', apply_status: 'applied', ui_status: 'applied', terminal: true })
    const result = await storeModule.refreshOperation('op-2')
    expect(api.fetch).toHaveBeenCalledWith('/panel-api/operations/op-2')
    expect(result.ui_status).toBe('applied')
  })

  it('replaces failed operations with accepted retry and rollback operations', async () => {
    storeModule.recordAcceptedOperation({
      operation_id: 'op-failed', agent_id: 'edge-1', desired_revision: 3, apply_status: 'degraded',
      agents: [
        { agent_id: 'edge-1', desired_revision: 3, apply_status: 'failed' },
        { agent_id: 'edge-2', desired_revision: 4, apply_status: 'failed' }
      ]
    })
    api.retry.mockResolvedValueOnce({
      operation_id: 'op-retry', agent_id: 'edge-1', desired_revision: 3, apply_status: 'pending'
    })
    api.rollback.mockResolvedValueOnce({
      operation_id: 'op-rollback', agent_id: 'edge-1', desired_revision: 4, apply_status: 'pending'
    })

    expect(await storeModule.retryOperation('op-failed', 'edge-2')).toMatchObject({ operation_id: 'op-retry', ui_status: 'pending' })
    expect(await storeModule.rollbackOperation('op-failed', 'edge-2')).toMatchObject({ operation_id: 'op-rollback', ui_status: 'pending' })
    expect(api.retry).toHaveBeenCalledWith(
      expect.objectContaining({ operation_id: 'op-failed' }),
      expect.objectContaining({ agent_id: 'edge-2', desired_revision: 4 })
    )
    expect(api.rollback).toHaveBeenCalledWith(
      expect.objectContaining({ operation_id: 'op-failed' }),
      expect.objectContaining({ agent_id: 'edge-2' })
    )
  })

  it('ignores an older concurrent status response that finishes last', async () => {
    storeModule.recordAcceptedOperation({
      operation_id: 'op-race', status_url: '/panel-api/operations/op-race', apply_status: 'pending'
    })
    let resolveFirst
    let resolveSecond
    api.fetch
      .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve }))
      .mockImplementationOnce(() => new Promise((resolve) => { resolveSecond = resolve }))

    const first = storeModule.refreshOperation('op-race')
    const second = storeModule.refreshOperation('op-race')
    resolveSecond({ operation_id: 'op-race', apply_status: 'drained', updated_at: '2026-07-15T12:00:02Z' })
    await second
    resolveFirst({ operation_id: 'op-race', apply_status: 'pending', updated_at: '2026-07-15T12:00:01Z' })
    await first

    expect(storeModule.useOperationsStore().get('op-race').ui_status).toBe('drained')
  })
})
