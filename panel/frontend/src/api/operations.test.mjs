import { beforeEach, describe, expect, it, vi } from 'vitest'

const requests = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ api: requests, longRunningRequest: { timeout: 0 } }))

const operations = await import('./operations.js')

describe('operation API contract', () => {
  beforeEach(() => {
    requests.get.mockReset()
    requests.post.mockReset()
  })

  it('preserves the resource and accepted operation metadata together', () => {
    const result = operations.preserveMutationEnvelope({
      rule: { id: 7, frontend_url: 'https://edge.test' },
      operation_id: 'operation-7',
      status_url: '/panel-api/operations/operation-7',
      agent_id: 'edge-1',
      desired_revision: 7,
      apply_status: 'pending'
    }, 'rule')

    expect(result).toMatchObject({
      id: 7,
      operation: {
        operation_id: 'operation-7',
        ui_status: 'pending',
        terminal: false
      }
    })
  })

  it('derives draining, drained, degraded and failure details from status facts', () => {
    expect(operations.normalizeOperationStatus({
      operation_id: 'op-applied',
      apply_status: 'applied',
      agents: [{ agent_id: 'edge-1', apply_status: 'applied' }]
    })).toMatchObject({ ui_status: 'applied', terminal: false })
    expect(operations.normalizeOperationStatus({
      operation_id: 'op-draining',
      apply_status: 'applied',
      agents: [{ drain_status: 'draining' }]
    }).ui_status).toBe('draining')
    expect(operations.normalizeOperationStatus({
      operation_id: 'op-drained',
      apply_status: 'applied',
      agents: [{ drain_status: 'drained' }]
    }).ui_status).toBe('drained')
    expect(operations.normalizeOperationStatus({
      operation_id: 'op-partial-drain',
      apply_status: 'applied',
      agents: [{ drain_status: 'drained' }, { drain_status: '' }]
    })).toMatchObject({ ui_status: 'applied', terminal: false })
    expect(operations.normalizeOperationStatus({
      operation_id: 'op-failed',
      apply_status: 'failed',
      error_code: 'apply_failed',
      error_message: 'bind failed'
    })).toMatchObject({ ui_status: 'failed', error_code: 'apply_failed', error_message: 'bind failed', terminal: true })
    expect(operations.normalizeOperationStatus({ operation_id: 'op-degraded', apply_status: 'applied', degraded: true }).ui_status).toBe('degraded')
  })

  it('uses status URL as the source of truth', async () => {
    requests.get.mockResolvedValueOnce({ data: { operation: {
      operation_id: 'op-1',
      primary_agent_id: 'edge-1',
      apply_status: 'applied',
      agents: [{ agent_id: 'edge-1', desired_revision: 8 }]
    } } })
    const result = await operations.fetchOperationStatus('/panel-api/operations/op-1')
    expect(requests.get).toHaveBeenCalledWith('/panel-api/operations/op-1')
    expect(result).toMatchObject({ ui_status: 'applied', agent_id: 'edge-1', desired_revision: 8 })
  })

  it('rejects absolute status URLs restored from untrusted local storage', async () => {
    await expect(operations.fetchOperationStatus('https://attacker.test/operations/op-1'))
      .rejects.toThrow('operation status URL is invalid')
    expect(requests.get).not.toHaveBeenCalled()
  })

  it('retries the failed member of a degraded multi-agent operation', async () => {
    requests.post.mockResolvedValueOnce({ data: {
      operation_id: 'op-retry', agent_id: 'edge-b', desired_revision: 9, apply_status: 'pending'
    } })

    await operations.retryRevision({
      operation_id: 'op-degraded',
      agent_id: 'edge-a',
      desired_revision: 8,
      agents: [
        { agent_id: 'edge-a', desired_revision: 8, apply_status: 'applied' },
        { agent_id: 'edge-b', desired_revision: 9, apply_status: 'failed' }
      ]
    })

    expect(requests.post).toHaveBeenCalledWith(
      '/agents/edge-b/revisions/9/retry',
      {},
      { timeout: 0 }
    )
  })
})
