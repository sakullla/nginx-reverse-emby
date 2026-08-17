import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchPluginDetail, fetchPluginLogs, invokePluginDynamicAction, publishPlugin } from './plugins'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ api: mocks, longRunningRequest: { timeout: 0 } }))

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

describe('plugin publish projection', () => {
  beforeEach(() => { mocks.get.mockReset(); mocks.post.mockReset() })

  it('returns the published entry projection from one plugin-side submit', async () => {
    mocks.post.mockResolvedValue({
      data: {
        result: {
          published_entries: [{
            rule_id: 12,
            agent_id: 'edge-a',
            frontend_url: 'https://user:secret@edge.example.com',
            enabled: true,
            accessible: false
          }]
        }
      }
    })

    const result = await publishPlugin('official.waf', {
      instance_id: 'official.waf-default',
      resource_group_id: 'rg-1',
      targets: ['edge-a'],
      policy_chains: [],
      frontend_url: 'https://edge.example.com'
    })

    expect(mocks.post).toHaveBeenCalledTimes(1)
    expect(mocks.post.mock.calls[0][0]).toBe('/plugins/official.waf/publish')
    expect(result.published_entries).toEqual([{
      rule_id: 12,
      agent_id: 'edge-a',
      frontend_url: 'https://user:[REDACTED]@edge.example.com',
      enabled: true,
      accessible: false
    }])
  })

  it('keeps published entry identity fields on plugin detail', async () => {
    mocks.get.mockResolvedValue({
      data: {
        published_entries: [{
          rule_id: 12,
          agent_id: 'edge-a',
          frontend_url: 'https://edge.example.com',
          enabled: false,
          accessible: false
        }],
        instances: [{
          bindings: [{ consumer: { kind: 'http_rule', id: '12' }, target_agent_id: 'edge-a' }]
        }]
      }
    })

    const detail = await fetchPluginDetail('official.waf')
    expect(detail.published_entries[0]).toMatchObject({
      rule_id: 12,
      agent_id: 'edge-a',
      frontend_url: 'https://edge.example.com',
      enabled: false,
      accessible: false
    })
    expect(detail.instances[0].bindings[0].consumer).toEqual({ kind: 'http_rule', id: '12' })
  })

  it('keeps published entries when instance bindings are derived rather than persisted', async () => {
    mocks.get.mockResolvedValue({
      data: {
        published_entries: [{
          rule_id: 12,
          agent_id: 'edge-a',
          frontend_url: 'https://edge.example.com',
          enabled: true,
          accessible: false
        }],
        instances: [{ bindings: [] }]
      }
    })

    const detail = await fetchPluginDetail('official.waf')
    expect(detail.published_entries[0]).toMatchObject({
      rule_id: 12,
      agent_id: 'edge-a',
      frontend_url: 'https://edge.example.com',
      enabled: true,
      accessible: false
    })
    expect(detail.instances[0].bindings).toEqual([])
  })
})
