// @vitest-environment node

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
    expect(requests.get).toHaveBeenCalledWith('/operations/op-1')
    expect(result).toMatchObject({ ui_status: 'applied', agent_id: 'edge-1', desired_revision: 8 })
  })

  it('records a completion timestamp when dismissing through the panel API', async () => {
    requests.post.mockResolvedValueOnce({ data: { operation: {
      operation_id: 'op-dismiss', apply_status: 'applying', completed_at: '2026-07-25T02:00:00Z'
    } } })

    const result = await operations.dismissOperationStatus('op-dismiss')

    expect(requests.post).toHaveBeenCalledWith('/operations/op-dismiss/dismiss', {})
    expect(result).toMatchObject({ ui_status: 'applying', completed_at: '2026-07-25T02:00:00Z', terminal: true })
    expect(result).not.toHaveProperty('dismissed')
    expect(result).not.toHaveProperty('dismissed_at')
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

  it('marks a completed applied no-op as terminal without drain rows', () => {
    const status = operations.normalizeOperationStatus({
      operation_id: 'op-noop', apply_status: 'applied', no_op: true, completed_at: '2026-07-18T12:00:00Z', agents: []
    })
    expect(status.ui_status).toBe('applied')
    expect(status.terminal).toBe(true)
  })

  it('keeps polling an applied operation while a predecessor is draining', () => {
    const status = operations.normalizeOperationStatus({
      operation_id: 'op-draining', apply_status: 'applied', completed_at: '2026-07-18T12:00:00Z',
      agents: [{ agent_id: 'edge-1', drain_status: 'draining' }]
    })
    expect(status.ui_status).toBe('draining')
    expect(status.terminal).toBe(false)
  })

  it('surfaces forced drain completion as a terminal forced result', () => {
    const status = operations.normalizeOperationStatus({
      operation_id: 'op-forced', apply_status: 'applied', completed_at: '2026-07-18T12:00:00Z',
      agents: [
        { agent_id: 'edge-1', drain_status: 'drained' },
        { agent_id: 'edge-2', drain_status: 'forced' }
      ]
    })
    expect(status.ui_status).toBe('forced')
    expect(status.terminal).toBe(true)
  })
})
