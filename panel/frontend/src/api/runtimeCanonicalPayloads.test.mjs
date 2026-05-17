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

  it('sends HTTP WireGuard entry payload only when enabled', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 8,
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
        frontend_url: 'https://app.example.test',
        backends: [{ url: 'http://origin.example.test' }],
        wireguard_entry_enabled: true,
        wireguard_profile_id: '101',
        wireguard_entry_listen_host: ' 10.8.0.1 ',
        wireguard_entry_listen_port: '18096'
      })
      await runtime.updateRule('edge-a', 8, {
        frontend_url: 'https://app.example.test',
        backends: [{ url: 'http://origin.example.test' }],
        wireguard_entry_enabled: false,
        wireguard_profile_id: 101,
        wireguard_entry_listen_host: '10.8.0.1',
        wireguard_entry_listen_port: 18096
      })

      expect(requests).toHaveLength(2)
      const enabledPayload = JSON.parse(requests[0].data)
      expect(enabledPayload.wireguard_entry_enabled).toBe(true)
      expect(enabledPayload.wireguard_profile_id).toBe(101)
      expect(enabledPayload.wireguard_entry_listen_host).toBe('10.8.0.1')
      expect(enabledPayload.wireguard_entry_listen_port).toBe(18096)

      const disabledPayload = JSON.parse(requests[1].data)
      expect(disabledPayload.wireguard_entry_enabled).toBe(false)
      expect(disabledPayload).not.toHaveProperty('wireguard_profile_id')
      expect(disabledPayload).not.toHaveProperty('wireguard_entry_listen_host')
      expect(disabledPayload).not.toHaveProperty('wireguard_entry_listen_port')
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('sends Relay listener WireGuard payloads with profile id and neutralized transport options', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          listener: {
            id: 11,
            name: 'wg-relay',
            transport_mode: 'wireguard',
            wireguard_profile_id: 101,
            obfs_mode: 'off',
            allow_transport_fallback: false
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

      await runtime.createRelayListener('edge-a', {
        name: 'wg-relay',
        transport_mode: 'wireguard',
        wireguard_profile_id: 101,
        obfs_mode: 'early_window_v2',
        allow_transport_fallback: true
      })
      await runtime.updateRelayListener('edge-a', 11, {
        name: 'wg-relay',
        transport_mode: 'wireguard',
        wireguard_profile_id: 101,
        obfs_mode: 'early_window_v2',
        allow_transport_fallback: true
      })

      expect(requests).toHaveLength(2)
      for (const request of requests) {
        const payload = JSON.parse(request.data)
        expect(payload.transport_mode).toBe('wireguard')
        expect(payload.wireguard_profile_id).toBe(101)
        expect(payload.obfs_mode).toBe('off')
        expect(payload.allow_transport_fallback).toBe(false)
      }
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('sends L4 WireGuard inbound payloads with listen mode and profile id', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 12,
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

      const created = await runtime.createL4Rule('edge-a', {
        protocol: 'udp',
        listen_host: '0.0.0.0',
        listen_port: 51820,
        listen_mode: 'wireguard',
        wireguard_inbound_mode: 'address',
        wireguard_profile_id: 101,
        wireguard_listen_host: '10.8.0.1',
        backends: [{ host: '10.8.0.2', port: 8080 }]
      })
      const updated = await runtime.updateL4Rule('edge-a', 12, {
        protocol: 'udp',
        listen_host: '0.0.0.0',
        listen_port: 51820,
        listen_mode: 'wireguard',
        wireguard_inbound_mode: 'address',
        wireguard_profile_id: 101,
        wireguard_listen_host: '10.8.0.1',
        backends: [{ host: '10.8.0.2', port: 8080 }]
      })

      expect(requests).toHaveLength(2)
      for (const request of requests) {
        const payload = JSON.parse(request.data)
        expect(payload.listen_mode).toBe('wireguard')
        expect(payload.wireguard_inbound_mode).toBe('address')
        expect(payload.wireguard_profile_id).toBe(101)
        expect(payload.wireguard_listen_host).toBe('10.8.0.1')
      }
      expect(created.listen_mode).toBe('wireguard')
      expect(updated.listen_mode).toBe('wireguard')
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('sends L4 WireGuard egress payloads with egress mode and profile id', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 13,
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

      const created = await runtime.createL4Rule('edge-a', {
        protocol: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 1080,
        listen_mode: 'proxy',
        proxy_egress_mode: 'wireguard',
        wireguard_profile_id: 102,
        proxy_entry_auth: { enabled: false, username: '', password: '' },
        backends: []
      })
      const updated = await runtime.updateL4Rule('edge-a', 13, {
        protocol: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 1080,
        listen_mode: 'proxy',
        proxy_egress_mode: 'wireguard',
        wireguard_profile_id: 102,
        proxy_entry_auth: { enabled: false, username: '', password: '' },
        backends: []
      })

      expect(requests).toHaveLength(2)
      for (const request of requests) {
        const payload = JSON.parse(request.data)
        expect(payload.listen_mode).toBe('proxy')
        expect(payload.proxy_egress_mode).toBe('wireguard')
        expect(payload.wireguard_profile_id).toBe(102)
      }
      expect(created.proxy_egress_mode).toBe('wireguard')
      expect(updated.proxy_egress_mode).toBe('wireguard')
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('sends L4 WireGuard egress URI payloads without profile id', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 15,
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

      const created = await runtime.createL4Rule('edge-a', {
        protocol: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 1080,
        listen_mode: 'wireguard',
        proxy_egress_mode: 'wireguard',
        wireguard_egress_uri: 'wireguard://endpoint.example.test?profile=egress',
        proxy_entry_auth: { enabled: false, username: '', password: '' },
        backends: []
      })
      const updated = await runtime.updateL4Rule('edge-a', 15, {
        protocol: 'tcp',
        listen_host: '0.0.0.0',
        listen_port: 1080,
        listen_mode: 'wireguard',
        proxy_egress_mode: 'wireguard',
        wireguard_egress_uri: 'wireguard://endpoint.example.test?profile=egress',
        proxy_entry_auth: { enabled: false, username: '', password: '' },
        backends: []
      })

      expect(requests).toHaveLength(2)
      for (const request of requests) {
        const payload = JSON.parse(request.data)
        expect(payload.listen_mode).toBe('wireguard')
        expect(payload.proxy_egress_mode).toBe('wireguard')
        expect(payload.wireguard_egress_uri).toBe('wireguard://endpoint.example.test?profile=egress')
        expect(payload).not.toHaveProperty('wireguard_profile_id')
      }
      expect(created.proxy_egress_mode).toBe('wireguard')
      expect(updated.proxy_egress_mode).toBe('wireguard')
      expect(created.wireguard_egress_uri).toBe('wireguard://endpoint.example.test?profile=egress')
      expect(updated.wireguard_egress_uri).toBe('wireguard://endpoint.example.test?profile=egress')
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('calls WireGuard URI preview and import endpoints', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      if (config.url === '/wireguard/parse-uri') {
        return {
          data: {
            ok: true,
            uri: 'wireguard://xxxxx@peer.example.test:51820?publickey=pub&psk=xxxxx&address=10.8.0.2%2F32#phone',
            profile: {
              name: 'phone',
              endpoint: 'peer.example.test:51820',
              public_key: 'pub',
              addresses: ['10.8.0.2/32'],
              allowed_ips: ['0.0.0.0/0', '::/0']
            }
          },
          status: 200,
          statusText: 'OK',
          headers: {},
          config
        }
      }
      return {
        data: {
          ok: true,
          profile: {
            id: 17,
            agent_id: 'edge-a',
            name: 'phone',
            mode: 'generic_wireguard',
            listen_port: 0,
            addresses: ['10.8.0.2/32'],
            peers: []
          }
        },
        status: 201,
        statusText: 'Created',
        headers: {},
        config
      }
    }

    try {
      const runtime = await vi.importActual('./runtime.js')
      const uri = 'wireguard://private@peer.example.test:51820?publickey=pub&psk=secret&address=10.8.0.2%2F32#phone'

      const preview = await runtime.parseWireGuardURI(uri)
      const profile = await runtime.importWireGuardURIProfile('edge-a', uri, 'fallback')

      expect(preview.profile.endpoint).toBe('peer.example.test:51820')
      expect(profile.id).toBe(17)
      expect(requests).toHaveLength(2)
      expect(requests[0].method).toBe('post')
      expect(requests[0].url).toBe('/wireguard/parse-uri')
      expect(JSON.parse(requests[0].data)).toEqual({ uri })
      expect(requests[1].method).toBe('post')
      expect(requests[1].url).toBe('/agents/edge-a/wireguard-profiles/import-uri')
      expect(JSON.parse(requests[1].data)).toEqual({ uri, name: 'fallback' })
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('calls WireGuard profile client endpoints and fetches config as text', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      if (config.url.endsWith('/config')) {
        return {
          data: '[Interface]\nAddress = 10.8.0.2/32\n',
          status: 200,
          statusText: 'OK',
          headers: { 'content-type': 'text/plain' },
          config
        }
      }
      if (String(config.url).includes('/uri')) {
        return {
          data: 'wireguard://client-private@wg.example.test:51820?publickey=server&address=10.8.0.2%2F32',
          status: 200,
          statusText: 'OK',
          headers: { 'content-type': 'text/plain' },
          config
        }
      }
      if (config.method === 'get') {
        return {
          data: {
            clients: [
              {
                id: 501,
                name: 'phone',
                address: '10.8.0.2/32',
                public_key: 'client-public-key',
                enabled: true
              }
            ]
          },
          status: 200,
          statusText: 'OK',
          headers: {},
          config
        }
      }
      if (config.method === 'post') {
        return {
          data: {
            client: {
              id: 502,
              ...JSON.parse(config.data)
            }
          },
          status: 201,
          statusText: 'Created',
          headers: {},
          config
        }
      }
      if (config.method === 'patch') {
        return {
          data: {
            client: {
              id: 502,
              name: 'phone',
              ...JSON.parse(config.data)
            }
          },
          status: 200,
          statusText: 'OK',
          headers: {},
          config
        }
      }
      return {
        data: {
          client: {
            id: 502,
            name: 'phone'
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
      const payload = {
        name: 'phone',
        allowed_ips: ['0.0.0.0/0', '::/0'],
        dns: ['1.1.1.1'],
        enabled: true
      }

      const clients = await runtime.fetchWireGuardClients('edge/a', 'profile 1')
      const client = await runtime.createWireGuardClient('edge/a', 'profile 1', payload)
      const updated = await runtime.updateWireGuardClient('edge/a', 'profile 1', 'client 1', { enabled: false })
      await runtime.deleteWireGuardClient('edge/a', 'profile 1', 'client 1')
      const configText = await runtime.fetchWireGuardClientConfig('edge/a', 'profile 1', 'client 1')
      const uriText = await runtime.fetchWireGuardClientURI('edge/a', 'profile 1', 'client 1', '1,2,3')

      expect(clients).toHaveLength(1)
      expect(client.name).toBe('phone')
      expect(updated.enabled).toBe(false)
      expect(configText).toContain('[Interface]')
      expect(uriText).toContain('wireguard://')
      expect(requests).toHaveLength(6)
      expect(requests[0].method).toBe('get')
      expect(requests[0].url).toBe('/agents/edge%2Fa/wireguard-profiles/profile%201/clients')
      expect(requests[1].method).toBe('post')
      expect(requests[1].url).toBe('/agents/edge%2Fa/wireguard-profiles/profile%201/clients')
      expect(JSON.parse(requests[1].data)).toEqual(payload)
      expect(JSON.parse(requests[1].data)).not.toHaveProperty('address')
      expect(JSON.parse(requests[1].data)).not.toHaveProperty('public_key')
      expect(requests[2].method).toBe('patch')
      expect(requests[2].url).toBe('/agents/edge%2Fa/wireguard-profiles/profile%201/clients/client%201')
      expect(JSON.parse(requests[2].data)).toEqual({ enabled: false })
      expect(requests[3].method).toBe('delete')
      expect(requests[3].url).toBe('/agents/edge%2Fa/wireguard-profiles/profile%201/clients/client%201')
      expect(requests[4].method).toBe('get')
      expect(requests[4].url).toBe('/agents/edge%2Fa/wireguard-profiles/profile%201/clients/client%201/config')
      expect(requests[4].responseType).toBe('text')
      expect(requests[5].method).toBe('get')
      expect(requests[5].url).toBe('/agents/edge%2Fa/wireguard-profiles/profile%201/clients/client%201/uri?reserved=1%2C2%2C3')
      expect(requests[5].responseType).toBe('text')
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('exports WireGuard URI helpers from API facades and dev mocks', async () => {
    const index = await import('./index.js?raw')
    const devRuntime = await import('./devRuntime.js?raw')
    const devMocks = await import('./devMocks/index.js?raw')
    const devData = await import('./devMocks/data.js?raw')

    for (const source of [index.default, devRuntime.default, devMocks.default, devData.default]) {
      expect(source).toContain('parseWireGuardURI')
      expect(source).toContain('importWireGuardURIProfile')
    }
  })

  it('exports WireGuard profile client helpers from API facades, dev mocks, and hooks', async () => {
    const index = await import('./index.js?raw')
    const devRuntime = await import('./devRuntime.js?raw')
    const devMocks = await import('./devMocks/index.js?raw')
    const devData = await import('./devMocks/data.js?raw')
    const hooks = await import('../hooks/useWireGuardProfiles.js?raw')

    for (const source of [index.default, devRuntime.default, devMocks.default, devData.default]) {
      expect(source).toContain('fetchWireGuardClients')
      expect(source).toContain('createWireGuardClient')
      expect(source).toContain('updateWireGuardClient')
      expect(source).toContain('deleteWireGuardClient')
      expect(source).toContain('fetchWireGuardClientConfig')
      expect(source).toContain('fetchWireGuardClientURI')
    }
    expect(hooks.default).toContain('useWireGuardClients')
    expect(hooks.default).toContain('useCreateWireGuardClient')
    expect(hooks.default).toContain('useUpdateWireGuardClient')
    expect(hooks.default).toContain('useDeleteWireGuardClient')
  })

  it('WireGuard Profiles page exposes clients workflow and keeps legacy peers advanced', async () => {
    const page = await import('../pages/WireGuardProfilesPage.vue?raw')
    const source = page.default

    expect(source).toContain('public_endpoint')
    expect(source).toContain('fetchWireGuardClients')
    expect(source).toContain('showClientForm')
    expect(source).toContain('handleCreateClient')
    expect(source).toContain('toggleClientEnabled')
    expect(source).toContain('isClientRowPending')
    expect(source).toContain('pendingClientRowIds')
    expect(source).toContain('deleteWireGuardClientRow')
    expect(source).toContain('downloadClientConfig')
    expect(source).toContain('copyClientURI')
    expect(source).toContain('clientForm.allowed_ips_text')
    expect(source).toContain('clientForm.dns_text')
    expect(source).not.toContain('clientForm.address')
    expect(source).not.toContain('clientForm.public_key')
    expect(source).toContain('messageStore.error(error, \'下载 WireGuard Client 配置失败\')')
    expect(source).toContain('高级 Legacy Peers')
    expect(source).toContain('<details')
    expect(source).not.toContain('client.private_key')
    expect(source).not.toContain('client.preshared_key')
  })

  it('WireGuard client payload omits blank DNS so profile DNS can be inherited', async () => {
    const page = await import('../pages/WireGuardProfilesPage.vue?raw')
    const source = page.default

    expect(source).toContain('clientForm.dns_text')
    expect(source).toContain('clientForm.value.dns_text.trim()')
    expect(source).not.toContain('dns: splitLines(clientForm.value.dns_text)')
  })

  it('WireGuard client mutations refresh clients and related WireGuard references for the raw target', async () => {
    const hooks = await import('../hooks/useWireGuardProfiles.js?raw')
    const source = hooks.default

    expect(source).toContain('invalidateWireGuardReferences(qc, rawAgentId)')
    expect(source).toContain("qc.invalidateQueries({ queryKey: ['wireGuardClients', rawAgentId, rawProfileId] })")
    expect(source).toContain('api.createWireGuardClient(rawAgentId, rawProfileId, payload)')
    expect(source).toContain('api.updateWireGuardClient(rawAgentId, rawProfileId, rawClientId, payload)')
    expect(source).toContain('api.deleteWireGuardClient(rawAgentId, rawProfileId, rawClientId)')
  })

  it('dev mock WireGuard clients follow create and update contracts while keeping list secrets private', async () => {
    const devData = await vi.importActual('./devMocks/data.js')
    const omittedDefaults = await devData.createWireGuardClient('local', 1, {
      name: 'watch'
    })
    const emptyAllowedIPs = await devData.createWireGuardClient('local', 1, {
      name: 'laptop',
      allowed_ips: []
    })
    const emptyDNS = await devData.createWireGuardClient('local', 1, {
      name: 'router',
      dns: []
    })
    const created = await devData.createWireGuardClient('local', 1, {
      name: 'tablet',
      allowed_ips: ['10.40.0.0/16'],
      dns: ['9.9.9.9'],
      enabled: false,
      address: '192.0.2.44/32',
      public_key: 'legacy-public-key'
    })
    const updated = await devData.updateWireGuardClient('local', 1, created.id, {
      enabled: true,
      name: 'renamed',
      allowed_ips: ['192.0.2.0/24'],
      dns: ['8.8.8.8']
    })
    const clients = await devData.fetchWireGuardClients('local', 1)
    const omittedDefaultsConfig = await devData.fetchWireGuardClientConfig('local', 1, omittedDefaults.id)
    const emptyDNSConfig = await devData.fetchWireGuardClientConfig('local', 1, emptyDNS.id)
    const initialClient = clients.find((client) => client.name === 'phone')
    const listed = clients.find((client) => client.id === created.id)

    expect(initialClient.allowed_ips).toEqual(['0.0.0.0/0', '::/0'])
    expect(omittedDefaults.allowed_ips).toEqual([omittedDefaults.address])
    expect(emptyAllowedIPs.allowed_ips).toEqual([emptyAllowedIPs.address])
    expect(omittedDefaults.dns).toEqual(['1.1.1.1'])
    expect(emptyDNS.dns).toEqual([])
    expect(omittedDefaultsConfig).toContain('DNS = 1.1.1.1')
    expect(emptyDNSConfig).not.toContain('DNS =')
    expect(created.name).toBe('tablet')
    expect(created.allowed_ips).toEqual(['10.40.0.0/16'])
    expect(created.dns).toEqual(['9.9.9.9'])
    expect(created.enabled).toBe(false)
    expect(updated.enabled).toBe(true)
    expect(updated.name).toBe('tablet')
    expect(updated.allowed_ips).toEqual(['10.40.0.0/16'])
    expect(updated.dns).toEqual(['9.9.9.9'])
    expect(updated.address).toBe(created.address)
    expect(updated.public_key).toBe(created.public_key)
    expect(created.address).not.toBe('192.0.2.44/32')
    expect(created.public_key).not.toBe('legacy-public-key')
    expect(created).not.toHaveProperty('private_key')
    expect(created).not.toHaveProperty('preshared_key')
    expect(listed).toBeTruthy()
    expect(listed).not.toHaveProperty('private_key')
    expect(listed).not.toHaveProperty('preshared_key')
    expect(omittedDefaults).not.toHaveProperty('private_key')
    expect(omittedDefaults).not.toHaveProperty('preshared_key')
    expect(emptyAllowedIPs).not.toHaveProperty('private_key')
    expect(emptyAllowedIPs).not.toHaveProperty('preshared_key')
    expect(emptyDNS).not.toHaveProperty('private_key')
    expect(emptyDNS).not.toHaveProperty('preshared_key')
  })

  it('dev mock WireGuard client update requires explicit boolean enabled', async () => {
    const devData = await vi.importActual('./devMocks/data.js')
    const client = await devData.createWireGuardClient('local', 1, {
      name: 'toggle-contract',
      enabled: true
    })

    await expect(devData.updateWireGuardClient('local', 1, client.id, {})).rejects.toThrow()
    await expect(devData.updateWireGuardClient('local', 1, client.id, { enabled: null })).rejects.toThrow()
    await expect(devData.updateWireGuardClient('local', 1, client.id, { enabled: 'false' })).rejects.toThrow()

    const disabled = await devData.updateWireGuardClient('local', 1, client.id, { enabled: false })
    const enabled = await devData.updateWireGuardClient('local', 1, client.id, { enabled: true })

    expect(disabled.enabled).toBe(false)
    expect(enabled.enabled).toBe(true)
  })

  it('dev mock WireGuard client create rejects invalid CIDR and DNS input', async () => {
    const devData = await vi.importActual('./devMocks/data.js')

    await expect(devData.createWireGuardClient('local', 1, {
      name: 'bad-cidr',
      allowed_ips: ['not-cidr']
    })).rejects.toThrow()
    await expect(devData.createWireGuardClient('local', 1, {
      name: 'bad-address',
      allowed_ips: ['999.1.1.1/32']
    })).rejects.toThrow()
    await expect(devData.createWireGuardClient('local', 1, {
      name: 'bad-dns-ip',
      dns: ['999.999.999.999']
    })).rejects.toThrow()
    await expect(devData.createWireGuardClient('local', 1, {
      name: 'bad-dns-host',
      dns: ['bad host name']
    })).rejects.toThrow()
  })

  it('dev mock WireGuard URI parsing preserves literal plus in keys and redacts secrets', async () => {
    const devData = await vi.importActual('./devMocks/data.js')
    const uri = 'wireguard://AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=@peer.example.test:51820?publickey=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB+&psk=CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC+&address=10.8.0.2%2F32#phone'

    const preview = await devData.parseWireGuardURI(uri)
    const profile = await devData.importWireGuardURIProfile('local', uri)

    expect(preview.profile.public_key).toBe('BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB+')
    expect(preview.uri).not.toContain('AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=')
    expect(preview.uri).not.toContain('CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC+')
    expect(profile.peers[0].public_key).toBe('BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB+')
    expect(profile.peers[0].preshared_key).toBe('xxxxx')
  })

  it('omits stale L4 WireGuard egress URI when egress is not WireGuard', async () => {
    const { api } = await vi.importActual('./client.js')
    const requests = []
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: {
          rule: {
            id: 16,
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
        listen_port: 1080,
        listen_mode: 'proxy',
        proxy_egress_mode: 'relay',
        wireguard_profile_id: 102,
        wireguard_egress_uri: 'wireguard://endpoint.example.test?profile=egress',
        proxy_entry_auth: { enabled: false, username: '', password: '' },
        relay_layers: [[7]],
        backends: []
      })

      const payload = JSON.parse(requests[0].data)
      expect(payload.proxy_egress_mode).toBe('relay')
      expect(payload).not.toHaveProperty('wireguard_egress_uri')
      expect(payload.wireguard_profile_id).toBe(102)
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('L4 form treats WireGuard listen mode as a proxy entry when egress is selected', async () => {
    const l4Form = await import('../components/L4RuleForm.vue?raw')

    expect(l4Form.default).toContain('const isProxyEntry = computed(() => form.value.protocol === \'tcp\' && (form.value.listen_mode === \'proxy\' || (form.value.listen_mode === \'wireguard\' && form.value.proxy_egress_mode !== \'\')))')
    expect(l4Form.default).toContain('const requiresBackends = computed(() => !isProxyEntry.value && !isWireGuardTransparentForward.value)')
    expect(l4Form.default).toContain('backends: requiresBackends.value ? validBackends : []')
    expect(l4Form.default).toContain('proxy_egress_mode: isProxyEntry.value ? form.value.proxy_egress_mode : \'\'')
    expect(l4Form.default).toContain('if (requiresBackends.value && validBackends.length === 0)')
  })

  it('L4 form exposes WireGuard egress URI source controls', async () => {
    const l4Form = await import('../components/L4RuleForm.vue?raw')

    expect(l4Form.default).toContain('wireguard_egress_source')
    expect(l4Form.default).toContain('wireguard_egress_uri')
    expect(l4Form.default).toContain('WireGuard 连接 URI')
    expect(l4Form.default).toContain('<option v-if="!isWireGuardInbound" value="uri">WireGuard URI</option>')
    expect(l4Form.default).toContain('payload.wireguard_egress_uri = form.value.wireguard_egress_uri.trim()')
    expect(l4Form.default).toContain('payload.wireguard_profile_id = selectedWireGuardProfileID.value')
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

  it('preserves L4 WireGuard proxy egress mode on read', async () => {
    const { api } = await vi.importActual('./client.js')
    const originalAdapter = api.defaults.adapter
    api.defaults.adapter = async (config) => ({
      data: {
        rules: [
          {
            id: 14,
            protocol: 'tcp',
            listen_host: '0.0.0.0',
            listen_port: 1080,
            listen_mode: 'wireguard',
            proxy_egress_mode: 'proxy',
            proxy_egress_url: 'socks5://127.0.0.1:1080',
            wireguard_profile_id: 101,
            wireguard_listen_host: '10.8.0.1',
            backends: []
          }
        ]
      },
      status: 200,
      statusText: 'OK',
      headers: {},
      config
    })

    try {
      const runtime = await vi.importActual('./runtime.js')

      const rules = await runtime.fetchL4Rules('edge-a')

      expect(rules[0].listen_mode).toBe('wireguard')
      expect(rules[0].proxy_egress_mode).toBe('proxy')
      expect(rules[0].proxy_egress_url).toBe('socks5://127.0.0.1:1080')
    } finally {
      api.defaults.adapter = originalAdapter
    }
  })

  it('Relay and L4 forms restrict WireGuard selection to enabled numeric profiles', async () => {
    const relayForm = await import('../components/RelayListenerForm.vue?raw')
    const l4Form = await import('../components/L4RuleForm.vue?raw')

    for (const source of [relayForm.default, l4Form.default]) {
      expect(source).toContain('enabledWireGuardProfiles')
      expect(source).toContain('selectedWireGuardProfileID')
      expect(source).toContain('Number.isInteger(id) && id > 0')
      expect(source).not.toContain('payload.wireguard_profile_id = String')
    }
  })

  it('Relay and L4 edit forms do not auto-replace invalid initial WireGuard profiles', async () => {
    const relayForm = await import('../components/RelayListenerForm.vue?raw')
    const l4Form = await import('../components/L4RuleForm.vue?raw')

    for (const source of [relayForm.default, l4Form.default]) {
      expect(source).toContain('wireGuardProfileHydratedFromInitialData')
      expect(source).toContain('wireGuardProfileRequiresExplicitSelection')
      expect(source).toContain('wireGuardProfileRequiresExplicitSelection.value = true')
      expect(source).toContain('wireGuardProfileRequiresExplicitSelection.value = false')
    }
  })

  it('L4 form sends WireGuard inbound mode and limits address listen host to address mode', async () => {
    const l4Form = await import('../components/L4RuleForm.vue?raw')

    expect(l4Form.default).toContain("wireguard_inbound_mode: initialData?.wireguard_inbound_mode === 'transparent' ? 'transparent' : 'address'")
    expect(l4Form.default).toContain('payload.wireguard_inbound_mode = form.value.wireguard_inbound_mode')
    expect(l4Form.default).toContain("if (isWireGuardInbound.value && form.value.wireguard_inbound_mode === 'address')")
    expect(l4Form.default).not.toContain('<option value="transparent">Transparent</option>')
  })

  it('HTTP form keeps WireGuard entry controls in the advanced tab', async () => {
    const ruleForm = await import('../components/RuleForm.vue?raw')
    const source = ruleForm.default
    const basicTabIndex = source.indexOf('<div v-if="activeTab === \'basic\'"')
    const advancedTabIndex = source.indexOf('<div v-else-if="activeTab === \'headers\'"')
    const relayTabIndex = source.indexOf('<div v-else-if="activeTab === \'relay\'"')
    const wireGuardControlIndex = source.indexOf('wireguard_entry_enabled')

    expect(basicTabIndex).toBeGreaterThanOrEqual(0)
    expect(advancedTabIndex).toBeGreaterThan(basicTabIndex)
    expect(relayTabIndex).toBeGreaterThan(advancedTabIndex)
    expect(wireGuardControlIndex).toBeGreaterThan(advancedTabIndex)
    expect(wireGuardControlIndex).toBeLessThan(relayTabIndex)
    expect(source.slice(basicTabIndex, advancedTabIndex)).not.toContain('wireguard_entry_enabled')
    expect(source).toContain('wireguard_profile_id')
    expect(source).toContain('wireguard_entry_listen_host')
    expect(source).toContain('wireguard_entry_listen_port')
    expect(source).toContain('enabledWireGuardProfiles')
    expect(source).toContain('selectedWireGuardProfileID')
  })
})
