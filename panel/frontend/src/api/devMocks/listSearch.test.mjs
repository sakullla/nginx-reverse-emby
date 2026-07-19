// @vitest-environment node

import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

let timeoutSpy

beforeAll(() => {
  timeoutSpy = vi.spyOn(globalThis, 'setTimeout').mockImplementation((callback, _delay, ...args) => {
    callback(...args)
    return 0
  })
})

afterAll(() => timeoutSpy.mockRestore())

vi.stubGlobal('localStorage', {
  getItem: vi.fn(() => null),
  setItem: vi.fn(),
  removeItem: vi.fn()
})

async function loadDevMocks() {
  vi.resetModules()
  return import('./data.js')
}

describe('dev mock list search', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubEnv('DEV', true)
  })

  it('filters L4 rules by protocol TCP without matching default listen_mode', async () => {
    const api = await loadDevMocks()
    const all = await api.fetchL4RulesPage({ page: 1, pageSize: 200, q: '' })
    const tcp = await api.fetchL4RulesPage({ page: 1, pageSize: 200, q: 'TCP' })
    const udp = await api.fetchL4RulesPage({ page: 1, pageSize: 200, q: 'UDP' })

    expect(all.total).toBeGreaterThan(0)
    expect(tcp.total).toBeGreaterThan(0)
    expect(tcp.total).toBeLessThan(all.total)
    expect(tcp.items.every((rule) => String(rule.protocol).toLowerCase() === 'tcp')).toBe(true)

    expect(udp.total).toBeGreaterThan(0)
    expect(udp.total).toBeLessThan(all.total)
    expect(udp.items.every((rule) => String(rule.protocol).toLowerCase() === 'udp')).toBe(true)
  })

  it('still matches L4 listen port and tags', async () => {
    const api = await loadDevMocks()
    const byPort = await api.fetchL4RulesPage({ page: 1, pageSize: 50, q: '25565' })
    expect(byPort.total).toBeGreaterThan(0)
    expect(byPort.items.some((rule) => Number(rule.listen_port) === 25565)).toBe(true)

    const byTag = await api.fetchL4RulesPage({ page: 1, pageSize: 50, q: 'game' })
    expect(byTag.total).toBeGreaterThan(0)
    expect(
      byTag.items.every((rule) => (rule.tags || []).some((tag) => String(tag).toLowerCase().includes('game')))
    ).toBe(true)
  })
})
