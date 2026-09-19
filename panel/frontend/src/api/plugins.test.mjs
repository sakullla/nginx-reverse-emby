import { beforeEach, describe, expect, it, vi } from 'vitest'

const del = vi.fn()
const get = vi.fn()
const post = vi.fn()
const longRunningRequest = { timeout: 0 }
vi.mock('./client', () => ({ api: { delete: del, get, post }, longRunningRequest }))

const plugins = await import('./plugins.js')

describe('plugin administration API', () => {
  beforeEach(() => {
    del.mockReset()
    get.mockReset()
    post.mockReset()
  })

  it('uses stable plugin lifecycle endpoints and envelopes', async () => {
    get.mockResolvedValueOnce({ data: { plugins: [{ plugin_id: 'official.waf' }] } })
    get.mockResolvedValueOnce({ data: { plugin: { plugin_id: 'official.waf' }, instances: [] } })
    get.mockResolvedValueOnce({ data: { operations: [{ id: 'op-1' }] } })
    post.mockResolvedValueOnce({ data: { package: { digest: 'a'.repeat(64) } } })
    post.mockResolvedValueOnce({ data: { plugin: { plugin_id: 'official.waf' } } })
    post.mockResolvedValueOnce({ data: { result: { current_lifecycle: 'active' } } })

    await expect(plugins.fetchPlugins()).resolves.toHaveLength(1)
    await plugins.fetchPluginDetail('official.waf')
    await plugins.fetchPluginOperations('official.waf')
    await plugins.fetchPluginPackageDetail({ source_id: 'official', plugin_id: 'official.waf', version: '1.0.0', digest: 'a'.repeat(64) })
    await plugins.installPlugin({ source_id: 'official', plugin_id: 'official.waf', version: '1.0.0', digest: 'a'.repeat(64), confirmed_permissions: [], risk_accepted: false })
    await plugins.enablePlugin('official.waf')

    expect(get).toHaveBeenNthCalledWith(1, '/plugins')
    expect(get).toHaveBeenNthCalledWith(2, '/plugins/official.waf', longRunningRequest)
    expect(get).toHaveBeenNthCalledWith(3, '/plugins/official.waf/operations')
    expect(post.mock.calls).toEqual([
      ['/plugins/package-detail', expect.any(Object), { timeout: plugins.PLUGIN_PACKAGE_DETAIL_TIMEOUT_MS }],
      ['/plugins/install', expect.any(Object), longRunningRequest],
      ['/plugins/official.waf/enable', {}, longRunningRequest]
    ])
  })

  it('deletes one encoded deployment instance', async () => {
    del.mockResolvedValue({ data: { deleted: true } })
    await expect(plugins.deletePluginInstance('official.rpc', 'instance/a')).resolves.toBe(true)
    expect(del).toHaveBeenCalledWith('/plugins/official.rpc/instances/instance%2Fa', longRunningRequest)
  })

  it('waits on the exact lifecycle operation before continuing', async () => {
    get.mockResolvedValueOnce({ data: { operations: [{ id: 'op-configure', status: 'succeeded', completed_at: '2026-08-23T10:55:12Z' }] } })
    await expect(plugins.waitForPluginOperation('official.rpc', 'op-configure')).resolves.toMatchObject({ status: 'succeeded' })
    expect(get).toHaveBeenCalledWith('/plugins/official.rpc/operations')
  })

  it('redacts secret-bearing read projections and rejects arbitrary actions', async () => {
    get.mockResolvedValue({ data: {
      plugin: {},
      package: { config_schema: { type: 'object', properties: {
        token: { type: 'string', title: 'ordinary token metadata' },
        api_credential: { type: 'string', writeOnly: true }
      } } },
      instances: [{ config: { api_credential: 'raw-secret', token: 'ordinary-token', mode: 'safe' }, secret_fields: [{ pointer: '/api_credential', present: true }] }],
      agent_statuses: [{ last_apply_message: 'authorization=raw' }]
    } })
    const detail = await plugins.fetchPluginDetail('team/plugin')
    expect(detail.instances[0].config).not.toHaveProperty('api_credential')
    expect(detail.instances[0].config.token).toBe('ordinary-token')
    expect(detail.instances[0].config.mode).toBe('safe')
    expect(detail.instances[0].secret_fields).toEqual([{ pointer: '/api_credential', present: true }])
    expect(detail.package.config_schema.properties.token.title).toBe('ordinary token metadata')
    expect(detail.agent_statuses[0].last_apply_message).toContain('[REDACTED]')
    await expect(plugins.runPluginAction('team/plugin', 'execute-script')).rejects.toThrow('plugin action is invalid')
    expect(get).toHaveBeenCalledWith('/plugins/team%2Fplugin', longRunningRequest)
  })
})

const publishedEntry = {
  rule_id: 7,
  instance_id: 'official.waf-default',
  agent_id: 'edge-a',
  frontend_url: 'https://media.example.com',
  enabled: true,
  accessible: true
}

const publishPayload = {
  instance_id: 'official.waf-default',
  resource_group_id: 'rg-1',
  targets: ['edge-a'],
  policy_chains: [],
  frontend_url: 'https://media.example.com',
  config: { mode: 'safe' },
  secret_replacements: {}
}

describe('plugin publish API', () => {
  beforeEach(() => {
    del.mockReset()
    get.mockReset()
    post.mockReset()
  })

  it('publishes one node and one enabled HTTP entry in a single plugin POST', async () => {
    post.mockResolvedValue({
      data: {
        result: {
          instance: {
            id: 'official.waf-default',
            targets: ['edge-a'],
            desired_enabled: true,
            bindings: [{ consumer: { kind: 'http_rule', id: '7' }, target_agent_id: 'edge-a' }]
          },
          published_entries: [publishedEntry]
        }
      }
    })

    const result = await plugins.publishPlugin('official.waf', publishPayload)

    expect(post).toHaveBeenCalledTimes(1)
    expect(post).toHaveBeenCalledWith('/plugins/official.waf/publish', publishPayload, longRunningRequest)
    const body = post.mock.calls[0][1]
    expect(body.targets).toEqual(['edge-a'])
    expect(body.frontend_url).toBe('https://media.example.com')
    expect(body).not.toHaveProperty('provider_id')
    expect(body).not.toHaveProperty('backends')
    expect(body).not.toHaveProperty('rule_id')
    expect(result.published_entries).toEqual([publishedEntry])
    expect(result.instance.bindings).toEqual([
      { consumer: { kind: 'http_rule', id: '7' }, target_agent_id: 'edge-a' }
    ])
  })

  it('updates an existing published entry by original rule id only', async () => {
    post.mockResolvedValue({
      data: {
        result: {
          published_entries: [{ ...publishedEntry, frontend_url: 'https://media-v2.example.com' }]
        }
      }
    })

    await plugins.publishPlugin('official.waf', {
      ...publishPayload,
      frontend_url: 'https://media-v2.example.com',
      rule_id: 7
    })

    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][0]).toBe('/plugins/official.waf/publish')
    expect(post.mock.calls[0][1].rule_id).toBe(7)
    expect(post.mock.calls[0][1].frontend_url).toBe('https://media-v2.example.com')
    expect(post.mock.calls[0][1].targets).toEqual(['edge-a'])
  })

  it('publishes another domain without a rule id so a separate entry can be created', async () => {
    post.mockResolvedValue({ data: { result: { published_entries: [{ ...publishedEntry, rule_id: 8, frontend_url: 'https://alt.example.com' }] } } })

    await plugins.publishPlugin('official.waf', {
      ...publishPayload,
      frontend_url: 'https://alt.example.com'
    })

    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][1]).not.toHaveProperty('rule_id')
    expect(post.mock.calls[0][1].frontend_url).toBe('https://alt.example.com')
  })

  it('unpublishes one published entry by original rule id and target', async () => {
    post.mockResolvedValue({ data: { result: { published_entries: [] } } })

    const result = await plugins.unpublishPlugin('official.waf', { targets: ['edge-a'], rule_id: 7 })

    expect(post).toHaveBeenCalledTimes(1)
    expect(post).toHaveBeenCalledWith('/plugins/official.waf/unpublish', { targets: ['edge-a'], rule_id: 7 }, longRunningRequest)
    expect(result.published_entries).toEqual([])
  })

  it('rejects unpublish without exactly one target or a positive rule id', async () => {
    await expect(plugins.unpublishPlugin('official.waf', { targets: ['edge-a', 'edge-b'], rule_id: 7 }))
      .rejects.toThrow('exactly one target is required')
    await expect(plugins.unpublishPlugin('official.waf', { targets: ['edge-a'], rule_id: 0 }))
      .rejects.toThrow('rule id is required')
    expect(post).not.toHaveBeenCalled()
  })

  it('rejects multiple targets or a missing domain before any write', async () => {
    await expect(plugins.publishPlugin('official.waf', { ...publishPayload, targets: ['edge-a', 'edge-b'] }))
      .rejects.toThrow('exactly one target is required')
    await expect(plugins.publishPlugin('official.waf', { ...publishPayload, targets: [] }))
      .rejects.toThrow('exactly one target is required')
    await expect(plugins.publishPlugin('official.waf', { ...publishPayload, frontend_url: '   ' }))
      .rejects.toThrow('frontend url is required')
    expect(post).not.toHaveBeenCalled()
  })

  it('does not fall back to configure or rule-page writes when publish is rejected', async () => {
    post.mockRejectedValue(new Error('plugin does not declare an HTTP backend'))

    await expect(plugins.publishPlugin('official.rpc', {
      ...publishPayload,
      instance_id: 'official.rpc-default'
    })).rejects.toThrow('plugin does not declare an HTTP backend')

    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][0]).toBe('/plugins/official.rpc/publish')
    expect(post.mock.calls.some((call) => String(call[0]).includes('/rules') || String(call[0]).endsWith('/configure'))).toBe(false)
  })

  it('projects published entries and http_rule bindings from plugin detail', async () => {
    get.mockResolvedValue({
      data: {
        plugin: { plugin_id: 'team/plugin' },
        published_entries: [{
          rule_id: 7,
          agent_id: 'edge-a',
          frontend_url: 'https://user:secret@media.example.com',
          enabled: true,
          accessible: false
        }],
        instances: [{
          id: 'team-plugin-default',
          bindings: [{ consumer: { kind: 'http_rule', id: '7' }, target_agent_id: 'edge-a' }],
          config: { mode: 'safe' },
          secret_fields: []
        }]
      }
    })

    const detail = await plugins.fetchPluginDetail('team/plugin')
    expect(get).toHaveBeenCalledWith('/plugins/team%2Fplugin', longRunningRequest)
    expect(detail.published_entries).toEqual([{
      rule_id: 7,
      agent_id: 'edge-a',
      frontend_url: 'https://user:[REDACTED]@media.example.com',
      enabled: true,
      accessible: false
    }])
    expect(detail.instances[0].bindings).toEqual([
      { consumer: { kind: 'http_rule', id: '7' }, target_agent_id: 'edge-a' }
    ])
  })

  it('keeps published entries when instance bindings are derived rather than persisted', async () => {
    get.mockResolvedValue({
      data: {
        plugin: { plugin_id: 'official.waf' },
        published_entries: [publishedEntry],
        instances: [{ id: 'official.waf-default', bindings: [] }]
      }
    })

    const detail = await plugins.fetchPluginDetail('official.waf')
    expect(detail.published_entries).toEqual([publishedEntry])
    expect(detail.instances[0].bindings).toEqual([])
  })
})
