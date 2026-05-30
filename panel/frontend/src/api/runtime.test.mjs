import { describe, expect, it, vi } from 'vitest'

const requests = vi.hoisted(() => ({
  post: vi.fn(),
  put: vi.fn()
}))

vi.mock('./client', () => ({
  api: {
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
