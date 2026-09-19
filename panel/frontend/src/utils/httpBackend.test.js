import { describe, expect, it } from 'vitest'
import { describeHTTPBackends } from './httpBackend.js'

describe('HTTP backend presentation', () => {
  it('projects provider identity and ready generation without runtime endpoint fields', () => {
    const result = describeHTTPBackends({
      backends: [{
        kind: 'plugin_provider',
        plugin_provider: { instance_id: 'accelerator-installed', provider_id: 'default' }
      }]
    }, [{
      instance_id: 'accelerator-installed',
      provider_id: 'default',
      display_name: '加速源',
      state: 'active',
      ready_generation: 'generation-9',
      socket: '/must/not/be/read'
    }])

    expect(result).toEqual([{
      kind: 'provider',
      label: '加速源',
      detail: 'accelerator-installed',
      state: 'active',
      generation: 'generation-9'
    }])
  })

  it('leaves URL backend presentation unchanged', () => {
    expect(describeHTTPBackends({ backends: [{ url: 'https://origin.example.net' }] })).toEqual([{
      kind: 'url', label: 'https://origin.example.net', detail: '', state: '', generation: ''
    }])
  })

  it('matches providers by Agent as well as instance and provider identity', () => {
    const catalog = [
      { agent_id: 'edge-a', instance_id: 'shared', provider_id: 'default', display_name: 'A', state: 'active', ready_generation: 'g-a' },
      { agent_id: 'edge-b', instance_id: 'shared', provider_id: 'default', display_name: 'B', state: 'inactive', ready_generation: 'g-b' }
    ]

    expect(describeHTTPBackends({
      agent_id: 'edge-b',
      backends: [{ kind: 'plugin_provider', plugin_provider: { instance_id: 'shared', provider_id: 'default' } }]
    }, catalog, 'ready')[0]).toMatchObject({ label: 'B', state: 'inactive', generation: 'g-b' })
  })

  it('distinguishes an unknown catalog from a ready catalog with a missing provider', () => {
    const rule = {
      agent_id: 'edge-a',
      backends: [{ kind: 'plugin_provider', plugin_provider: { instance_id: 'missing', provider_id: 'default' } }]
    }

    expect(describeHTTPBackends(rule, [], 'loading')[0].state).toBe('unknown')
    expect(describeHTTPBackends(rule, [], 'error')[0].state).toBe('unknown')
    expect(describeHTTPBackends(rule, [], 'ready')[0].state).toBe('unavailable')
  })
})
