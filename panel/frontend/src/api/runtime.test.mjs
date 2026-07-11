import { beforeEach, describe, expect, it, vi } from 'vitest'

const requests = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn()
}))

vi.mock('./client', () => ({
  api: {
    get: requests.get,
    post: requests.post,
    put: requests.put
  },
  longRunningRequest: { timeout: 0 }
}))

const runtime = await import('./runtime.js')

describe('runtime egress profile payload normalization', () => {
  it('keeps explicit direct HTTP egress selection on rule updates', async () => {
    requests.put.mockResolvedValueOnce({ data: { rule: { id: 7, frontend_url: 'https://app.example.test' } } })

    await runtime.updateRule('local', 7, {
      frontend_url: 'https://app.example.test',
      backends: [{ url: 'http://origin.example.test:8096' }],
      egress_profile_id: 0
    })

    expect(requests.put.mock.calls[0][1]).toMatchObject({
      egress_profile_id: 0
    })
  })

  it('keeps explicit direct L4 egress selection on rule updates', async () => {
    requests.put.mockResolvedValueOnce({ data: { rule: { id: 9, protocol: 'tcp', backends: [] } } })

    await runtime.updateL4Rule('local', 9, {
      protocol: 'tcp',
      listen_host: '0.0.0.0',
      listen_port: 25565,
      backends: [{ host: 'origin.example.test', port: 25565 }],
      egress_profile_id: 0
    })

    expect(requests.put.mock.calls[0][1]).toMatchObject({
      egress_profile_id: 0
    })
  })
})

describe('buildListQueryParams', () => {
  it('omits agent_id for blank and all-agents filters', () => {
    expect(runtime.buildListQueryParams({})).toEqual({ page: 1, page_size: 20 })
    expect(runtime.buildListQueryParams({ agentId: '' })).toEqual({ page: 1, page_size: 20 })
    expect(runtime.buildListQueryParams({ agentFilter: '__all__' })).toEqual({ page: 1, page_size: 20 })
    expect(runtime.buildListQueryParams({ agentId: 'all' })).toEqual({ page: 1, page_size: 20 })
    expect(runtime.buildListQueryParams({ agentId: '*' })).toEqual({ page: 1, page_size: 20 })
  })

  it('includes concrete agent_id, page, page_size, and q', () => {
    expect(runtime.buildListQueryParams({
      agentId: 'edge',
      page: 2,
      pageSize: 50,
      q: ' app '
    })).toEqual({
      agent_id: 'edge',
      page: 2,
      page_size: 50,
      q: 'app'
    })
  })

  it('defaults invalid page/pageSize', () => {
    expect(runtime.buildListQueryParams({ page: 0, pageSize: -1 })).toEqual({
      page: 1,
      page_size: 20
    })
  })
})

describe('normalizeListPageResponse', () => {
  it('maps collection key items and pagination meta', () => {
    const page = runtime.normalizeListPageResponse({
      ok: true,
      rules: [{ id: 1, agent_id: 'local', frontend_url: 'https://a.test' }],
      total: 3,
      page: 2,
      page_size: 1
    }, 'rules')

    expect(page).toEqual({
      items: [{ id: 1, agent_id: 'local', frontend_url: 'https://a.test' }],
      total: 3,
      page: 2,
      page_size: 1
    })
  })

  it('applies item normalizer for HTTP rules', () => {
    const page = runtime.normalizeListPageResponse({
      rules: [{ id: 1, agent_id: 'edge', frontend_url: 'https://x.test', backends: [{ url: 'http://b' }] }],
      total: 1,
      page: 1,
      page_size: 20
    }, 'rules', (rule) => ({ ...rule, normalized: true }))

    expect(page.items[0]).toMatchObject({
      id: 1,
      agent_id: 'edge',
      normalized: true
    })
  })
})

describe('paginated list fetchers', () => {
  beforeEach(() => {
    requests.get.mockReset()
  })

  it('fetchHttpRulesPage sends query params and normalizes envelope', async () => {
    requests.get.mockResolvedValueOnce({
      data: {
        ok: true,
        rules: [{
          id: 1,
          agent_id: 'edge',
          agent_name: 'Edge',
          frontend_url: 'https://edge.example.test',
          backends: [{ url: 'http://origin:8096' }]
        }],
        total: 1,
        page: 1,
        page_size: 20
      }
    })

    const result = await runtime.fetchHttpRulesPage({ agentId: 'edge', page: 1, pageSize: 20 })

    expect(requests.get).toHaveBeenCalledWith('/http-rules', {
      params: { agent_id: 'edge', page: 1, page_size: 20 }
    })
    expect(result.total).toBe(1)
    expect(result.page).toBe(1)
    expect(result.page_size).toBe(20)
    expect(result.items).toHaveLength(1)
    expect(result.items[0]).toMatchObject({
      id: 1,
      agent_id: 'edge',
      agent_name: 'Edge'
    })
    expect(result.items[0].backends).toEqual([{ url: 'http://origin:8096' }])
  })

  it('fetchHttpRulesPage omits agent_id when listing all agents', async () => {
    requests.get.mockResolvedValueOnce({
      data: { ok: true, rules: [], total: 0, page: 1, page_size: 20 }
    })

    await runtime.fetchHttpRulesPage({ agentFilter: '__all__', page: 1, pageSize: 20, q: 'emby' })

    expect(requests.get).toHaveBeenCalledWith('/http-rules', {
      params: { page: 1, page_size: 20, q: 'emby' }
    })
  })

  it('fetchCertificatesPage always includes page so backend uses ListPage path', async () => {
    requests.get.mockResolvedValueOnce({
      data: {
        ok: true,
        certificates: [{ id: 9, domain: 'a.test', agent_id: 'local' }],
        total: 1,
        page: 1,
        page_size: 10
      }
    })

    const result = await runtime.fetchCertificatesPage({ page: 1, pageSize: 10 })

    expect(requests.get).toHaveBeenCalledWith('/certificates', {
      params: { page: 1, page_size: 10 }
    })
    expect(result.items[0]).toMatchObject({ id: 9, agent_id: 'local' })
  })

  it('fetchL4RulesPage / relay / wireguard hit the correct collection keys', async () => {
    requests.get
      .mockResolvedValueOnce({
        data: { ok: true, rules: [{ id: 2, agent_id: 'local', protocol: 'tcp', backends: [] }], total: 1, page: 1, page_size: 20 }
      })
      .mockResolvedValueOnce({
        data: { ok: true, listeners: [{ id: 3, agent_id: 'edge', name: 'r1' }], total: 1, page: 1, page_size: 20 }
      })
      .mockResolvedValueOnce({
        data: { ok: true, profiles: [{ id: 4, agent_id: 'local', name: 'wg1' }], total: 1, page: 1, page_size: 20 }
      })

    const l4 = await runtime.fetchL4RulesPage({ agentId: 'local' })
    const relay = await runtime.fetchRelayListenersPage({ agentId: 'edge' })
    const wg = await runtime.fetchWireGuardProfilesPage({ agentId: 'local' })

    expect(requests.get.mock.calls[0][0]).toBe('/l4-rules')
    expect(requests.get.mock.calls[1][0]).toBe('/relay-listeners')
    expect(requests.get.mock.calls[2][0]).toBe('/wireguard-profiles')
    expect(l4.items[0].agent_id).toBe('local')
    expect(relay.items[0].agent_id).toBe('edge')
    expect(wg.items[0].agent_id).toBe('local')
  })
})
