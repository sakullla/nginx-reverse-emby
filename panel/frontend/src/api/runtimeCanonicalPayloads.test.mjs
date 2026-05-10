import { beforeEach, describe, expect, it, vi } from 'vitest'

describe('runtime canonical rule payloads', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  it('sends HTTP save payloads with backends and relay_layers only', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 7,
            frontend_url: 'https://app.example.test',
            backends: [{ url: 'http://origin.example.test' }],
            relay_layers: [[101]]
          }
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config
      }
    }

    try {
      const runtime = await vi.importActual('./runtime.js')

      await runtime.createRule('edge-a', {
        frontend_url: 'https://app.example.test',
        backends: [{ url: 'http://origin.example.test' }],
        relay_layers: [[101]],
        backend_url: 'http://legacy.example.test',
        relay_chain: [101]
      })
      await runtime.updateRule('edge-a', 7, {
        frontend_url: 'https://app.example.test',
        backends: [{ url: 'http://origin.example.test' }],
        relay_layers: [[101]],
        backend_url: 'http://legacy.example.test',
        relay_chain: [101]
      })

      expect(requests).toHaveLength(2)
      for (const request of requests) {
        const payload = JSON.parse(request.data)
        expect(payload.backends).toEqual([{ url: 'http://origin.example.test' }])
        expect(payload.relay_layers).toEqual([[101]])
        expect(payload).not.toHaveProperty('backend_url')
        expect(payload).not.toHaveProperty('relay_chain')
      }
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('sends L4 save payloads with backends and relay_layers only', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 9,
            protocol: 'tcp',
            listen_host: '0.0.0.0',
            listen_port: 443,
            backends: [{ host: '10.0.0.1', port: 25565 }],
            relay_layers: [[201]]
          }
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config
      }
    }

    try {
      const runtime = await vi.importActual('./runtime.js')

      await runtime.createL4Rule('edge-a', {
        protocol: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 443,
        backends: [{ host: '10.0.0.1', port: 25565 }],
        relay_layers: [[201]],
        upstream_host: '10.0.0.1',
        upstream_port: 25565,
        relay_chain: [201]
      })
      await runtime.updateL4Rule('edge-a', 9, {
        protocol: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 443,
        backends: [{ host: '10.0.0.1', port: 25565 }],
        relay_layers: [[201]],
        upstream_host: '10.0.0.1',
        upstream_port: 25565,
        relay_chain: [201]
      })

      expect(requests).toHaveLength(2)
      for (const request of requests) {
        const payload = JSON.parse(request.data)
        expect(payload.backends).toEqual([{ host: '10.0.0.1', port: 25565 }])
        expect(payload.relay_layers).toEqual([[201]])
        expect(payload).not.toHaveProperty('upstream_host')
        expect(payload).not.toHaveProperty('upstream_port')
        expect(payload).not.toHaveProperty('relay_chain')
      }
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('does not synthesize canonical backends from legacy runtime fields', async () => {
    const { api } = await vi.importActual('./client.js')
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      if (config.url.endsWith('/rules')) {
        return {
          data: {
            rules: [
              {
                id: 7,
                frontend_url: 'https://app.example.test',
                backend_url: 'http://legacy.example.test',
                relay_chain: [101]
              }
            ]
          },
          status: 200,
          statusText: 'OK',
          headers: {},
          config
        }
      }

      return {
        data: {
          rules: [
            {
              id: 9,
              protocol: 'tcp',
              listen_host: '0.0.0.0',
              listen_port: 443,
              upstream_host: '10.0.0.1',
              upstream_port: 25565,
              relay_chain: [201]
            }
          ]
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config
      }
    }

    try {
      const runtime = await vi.importActual('./runtime.js')

      const rules = await runtime.fetchRules('edge-a')
      const l4Rules = await runtime.fetchL4Rules('edge-a')

      expect(rules[0].backends).toEqual([])
      expect(l4Rules[0].backends).toEqual([])
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })
})
