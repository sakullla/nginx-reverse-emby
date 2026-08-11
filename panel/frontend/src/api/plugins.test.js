import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchPluginLogs, invokePluginDynamicAction } from './plugins'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ api: mocks }))

describe('plugin dynamic action and log API', () => {
  beforeEach(() => { mocks.get.mockReset(); mocks.post.mockReset() })

  it('sends only host action fields with a stable opaque retry key', async () => {
    mocks.post.mockResolvedValue({ data: { operation_id: 'server-operation', replayed: false } })
    await invokePluginDynamicAction('official.rpc', 'instance-a', 'rotate', 'relay-a', true, 'ui:stable-opaque-key-0001')
    expect(mocks.post).toHaveBeenCalledWith('/plugins/official.rpc/instances/instance-a/actions/rotate', { target_id: 'relay-a', confirmed: true }, { headers: { 'Idempotency-Key': 'ui:stable-opaque-key-0001' } })
    const body = mocks.post.mock.calls[0][1]
    expect(body).not.toHaveProperty('operation_id')
    expect(body).not.toHaveProperty('resource_group_id')
    expect(body).not.toHaveProperty('target_kind')
  })

  it('uses bounded cursor and Agent query fields for logs', async () => {
    mocks.get.mockResolvedValue({ data: { entries: [{ message: 'token=[REDACTED]' }], next_cursor: 'next' } })
    const page = await fetchPluginLogs('official.rpc', 'instance-a', { agentID: 'edge-a', cursor: 'cursor', limit: 25 })
    expect(mocks.get).toHaveBeenCalledWith('/plugins/official.rpc/instances/instance-a/logs', { params: { agent_id: 'edge-a', cursor: 'cursor', limit: 25 } })
    expect(page.entries[0].message).toBe('token=[REDACTED]')
  })
})
