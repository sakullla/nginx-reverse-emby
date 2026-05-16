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

  it('L4 form treats WireGuard listen mode as a proxy entry when egress is selected', async () => {
    const l4Form = await import('../components/L4RuleForm.vue?raw')

    expect(l4Form.default).toContain('const isProxyEntry = computed(() => form.value.protocol === \'tcp\' && (form.value.listen_mode === \'proxy\' || (form.value.listen_mode === \'wireguard\' && form.value.proxy_egress_mode !== \'\')))')
    expect(l4Form.default).toContain('proxy_egress_mode: isProxyEntry.value ? form.value.proxy_egress_mode : \'\'')
    expect(l4Form.default).toContain('if (!isProxyEntry.value && validBackends.length === 0)')
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
