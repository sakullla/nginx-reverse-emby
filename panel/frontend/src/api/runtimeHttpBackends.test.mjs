import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createRule, fetchHTTPBackendProviders, fetchRules } from './runtime.js'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('./client', () => ({ api: mocks, longRunningRequest: { timeout: 0 } }))

describe('HTTP backend API wire contract', () => {
  beforeEach(() => {
    mocks.get.mockReset()
    mocks.post.mockReset()
  })

  it('preserves historical URL entries and canonical provider references on read', async () => {
    mocks.get.mockResolvedValue({
      data: {
        rules: [{
          id: 7,
          backends: [
            { url: ' https://origin.example.net ' },
            { kind: 'plugin_provider', plugin_provider: { instance_id: ' accelerator ', provider_id: ' default ' } }
          ]
        }]
      }
    })

    const rules = await fetchRules('edge-a')
    expect(rules[0].backends).toEqual([
      { url: 'https://origin.example.net' },
      {
        kind: 'plugin_provider',
        plugin_provider: { instance_id: 'accelerator', provider_id: 'default' }
      }
    ])
    expect(rules[0].backends[0]).not.toHaveProperty('kind')
  })

  it('does not retag URL entries while sending a provider rule', async () => {
    mocks.post.mockResolvedValue({ data: { rule: { id: 8, backends: [] } } })
    await createRule('edge-a', {
      frontend_url: 'https://media.example.com',
      backends: [
        { url: 'https://origin.example.net' },
        { kind: 'plugin_provider', plugin_provider: { instance_id: 'accelerator', provider_id: 'default' } }
      ]
    })

    expect(mocks.post.mock.calls[0][1].backends).toEqual([
      { url: 'https://origin.example.net' },
      {
        kind: 'plugin_provider',
        plugin_provider: { instance_id: 'accelerator', provider_id: 'default' }
      }
    ])
  })

  it('loads only the safe provider catalog projection for the selected Agent', async () => {
    const providers = [{
      instance_id: 'accelerator', provider_id: 'default', display_name: '加速源',
      agent_id: 'edge/a', ready_generation: 'generation-3', state: 'active'
    }]
    mocks.get.mockResolvedValue({ data: { providers } })

    await expect(fetchHTTPBackendProviders('edge/a')).resolves.toEqual(providers)
    expect(mocks.get).toHaveBeenCalledWith('/agents/edge%2Fa/http-backend-providers')
  })
})
