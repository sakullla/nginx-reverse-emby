import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.stubGlobal('localStorage', {
  getItem: vi.fn(() => null),
  setItem: vi.fn(),
  removeItem: vi.fn()
})

async function loadDevMocks() {
  vi.resetModules()
  return import('./data.js')
}

describe('dev WireGuard profile mocks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubEnv('DEV', true)
  })

  it('allows creating a generated-client bootstrap profile without legacy peers', async () => {
    const api = await loadDevMocks()

    const profile = await api.createWireGuardProfile('local', {
      name: 'bootstrap',
      mode: 'generic_wireguard',
      private_key: 'xxxxx',
      listen_port: 51830,
      addresses: ['0.0.0.0'],
      interface_addresses: ['10.8.10.1/24'],
      peers: [],
      enabled: true
    })

    expect(profile.peers).toEqual([])
    expect(profile.enabled).toBe(true)
  })

  it('preserves enabled generated-client peers when profile edits send empty legacy peers', async () => {
    const api = await loadDevMocks()

    const profile = await api.createWireGuardProfile('local', {
      name: 'client-owned',
      mode: 'generic_wireguard',
      private_key: 'xxxxx',
      listen_port: 51831,
      addresses: ['0.0.0.0'],
      interface_addresses: ['10.8.11.1/24'],
      peers: [],
      enabled: true
    })
    const client = await api.createWireGuardClient('local', profile.id, { name: 'phone' })

    const updated = await api.updateWireGuardProfile('local', profile.id, {
      name: 'client-owned-renamed',
      private_key: 'xxxxx',
      listen_port: 51831,
      addresses: ['0.0.0.0'],
      interface_addresses: ['10.8.11.1/24'],
      peers: [],
      enabled: true
    })

    expect(updated.peers).toHaveLength(1)
    expect(updated.peers[0]).toMatchObject({
      name: client.name,
      public_key: client.public_key,
      allowed_ips: [client.address]
    })
  })
})
