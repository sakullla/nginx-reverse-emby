import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
const post = vi.fn()
const longRunningRequest = { timeout: 0 }
vi.mock('./client', () => ({ api: { get, post }, longRunningRequest }))

const plugins = await import('./plugins.js')

describe('plugin administration API', () => {
  beforeEach(() => {
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
      ['/plugins/package-detail', expect.any(Object), longRunningRequest],
      ['/plugins/install', expect.any(Object), longRunningRequest],
      ['/plugins/official.waf/enable', {}, longRunningRequest]
    ])
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
