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
})
