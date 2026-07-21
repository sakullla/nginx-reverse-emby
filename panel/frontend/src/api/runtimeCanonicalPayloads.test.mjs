// @vitest-environment node

import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'

describe('runtime canonical rule payloads', () => {
  let timeoutSpy

  beforeAll(() => {
    timeoutSpy = vi.spyOn(globalThis, 'setTimeout').mockImplementation((callback, _delay, ...args) => {
      callback(...args)
      return 0
    })
  })

  afterAll(() => {
    timeoutSpy.mockRestore()
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

  it('sends HTTP egress profile id payloads', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 17,
            ...JSON.parse(config.data)
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
        frontend_url: 'https://media.example.test',
        backends: [{ url: 'http://10.0.0.9:8096' }],
        egress_profile_id: '17'
      })
      await runtime.updateRule('edge-a', 17, {
        frontend_url: 'https://media.example.test',
        backends: [{ url: 'http://10.0.0.9:8096' }],
        egress_profile_id: 0
      })

      expect(requests).toHaveLength(2)
      const createPayload = JSON.parse(requests[0].data)
      const updatePayload = JSON.parse(requests[1].data)
      expect(createPayload.egress_profile_id).toBe(17)
      expect(updatePayload.egress_profile_id).toBe(0)
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('sends L4 egress profile id payloads', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 23,
            ...JSON.parse(config.data)
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
        listen_port: 25565,
        backends: [{ host: '10.0.0.9', port: 25565 }],
        egress_profile_id: '23'
      })
      await runtime.updateL4Rule('edge-a', 23, {
        protocol: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 25565,
        backends: [{ host: '10.0.0.9', port: 25565 }],
        egress_profile_id: -1
      })

      expect(requests).toHaveLength(2)
      const createPayload = JSON.parse(requests[0].data)
      const updatePayload = JSON.parse(requests[1].data)
      expect(createPayload.egress_profile_id).toBe(23)
      expect(updatePayload).not.toHaveProperty('egress_profile_id')
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('calls egress profile CRUD endpoints', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      if (config.method === 'get') {
        return {
          data: { profiles: [{ id: 17, name: 'office socks', type: 'socks', proxy_url: 'socks5://127.0.0.1:1080', enabled: true }] },
          status: 200,
          statusText: 'OK',
          headers: {},
          config
        }
      }
      return {
        data: { profile: { id: 17, ...(config.data ? JSON.parse(config.data) : {}) } },
        status: config.method === 'post' ? 201 : 200,
        statusText: 'OK',
        headers: {},
        config
      }
    }

    try {
      const runtime = await vi.importActual('./runtime.js')

      const profiles = await runtime.fetchEgressProfiles()
      const created = await runtime.createEgressProfile({
        name: 'office socks',
        type: 'socks',
        proxy_url: ' socks5://127.0.0.1:1080 ',
        enabled: true
      })
      const updated = await runtime.updateEgressProfile(17, {
        name: 'direct',
        type: 'direct',
        proxy_url: 'socks5://127.0.0.1:1080',
        enabled: false
      })
      await runtime.deleteEgressProfile(17)

      expect(profiles).toHaveLength(1)
      expect(created.proxy_url).toBe('socks5://127.0.0.1:1080')
      expect(updated.proxy_url).toBe('')
      expect(requests.map((request) => [request.method, request.url])).toEqual([
        ['get', '/egress-profiles'],
        ['post', '/egress-profiles'],
        ['put', '/egress-profiles/17'],
        ['delete', '/egress-profiles/17']
      ])
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('dev mock egress profile CRUD preserves ordinary profile contracts', async () => {
    const devData = await vi.importActual('./devMocks/data.js')

    const before = await devData.fetchEgressProfiles()
    const created = await devData.createEgressProfile({
      name: 'office http',
      type: 'http',
      proxy_url: ' http://127.0.0.1:8080 ',
      enabled: true
    })
    const updated = await devData.updateEgressProfile(created.id, {
      name: 'direct',
      type: 'direct',
      proxy_url: 'http://127.0.0.1:8080',
      enabled: false
    })
    const deleted = await devData.deleteEgressProfile(created.id)
    const after = await devData.fetchEgressProfiles()

    expect(before.some((profile) => profile.id === created.id)).toBe(false)
    expect(created.type).toBe('http')
    expect(created.proxy_url).toBe('http://127.0.0.1:8080')
    expect(updated.type).toBe('direct')
    expect(updated.proxy_url).toBe('')
    expect(deleted.id).toBe(created.id)
    expect(after.some((profile) => profile.id === created.id)).toBe(false)
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
