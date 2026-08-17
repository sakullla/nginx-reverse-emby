// @vitest-environment node

import { beforeEach, describe, expect, it, vi } from 'vitest'

const requests = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn()
}))

vi.mock('./client', () => ({
  api: {
    get: requests.get,
    post: requests.post
  },
  longRunningRequest: { timeout: 0 }
}))

const runtime = await import('./runtime.js')

describe('cloudflare dns mapping API', () => {
  beforeEach(() => {
    requests.get.mockReset()
    requests.post.mockReset()
  })

  it('drops token material from list and detail payloads', async () => {
    requests.get.mockResolvedValueOnce({
      data: {
        mappings: [{ suffix: 'example.com', configured: true, updated_at: 9, token: 'secret-a' }],
        access: { can_read: true, can_write: true, can_rotate: false, token: 'secret-b' }
      }
    })
    requests.get.mockResolvedValueOnce({
      data: { mapping: { suffix: 'example.com', configured: true, updated_at: 9, token: 'secret-c' } }
    })

    const listed = await runtime.fetchCloudflareDnsMappings()
    const detail = await runtime.fetchCloudflareDnsMapping('example.com')

    expect(listed.mappings).toEqual([{ suffix: 'example.com', configured: true, updated_at: 9 }])
    expect(listed.access).toEqual({ can_read: true, can_write: true, can_rotate: false })
    expect(detail).toEqual({ suffix: 'example.com', configured: true, updated_at: 9 })
    expect(JSON.stringify(listed)).not.toContain('secret')
    expect(JSON.stringify(detail)).not.toContain('secret')
    expect(requests.get).toHaveBeenNthCalledWith(1, '/cloudflare-dns/api/mappings')
    expect(requests.get).toHaveBeenNthCalledWith(2, '/cloudflare-dns/api/mappings/example.com')
  })

  it('posts create, rename, rotate, and delete against the mounted plugin API', async () => {
    requests.post.mockResolvedValue({ data: { mapping: { suffix: 'example.com', configured: true, updated_at: 1 } } })

    await runtime.createCloudflareDnsMapping({ suffix: 'example.com', token: 'write-only' })
    await runtime.renameCloudflareDnsMapping('example.com', 'www.example.com')
    await runtime.rotateCloudflareDnsMapping('example.com', 'rotated')
    await runtime.deleteCloudflareDnsMapping('example.com')

    expect(requests.post.mock.calls).toEqual([
      ['/cloudflare-dns/api/mappings', { suffix: 'example.com', token: 'write-only' }],
      ['/cloudflare-dns/api/mappings/example.com/rename', { suffix: 'www.example.com', confirm: 'example.com' }],
      ['/cloudflare-dns/api/mappings/example.com/rotate', { token: 'rotated', confirm: 'example.com' }],
      ['/cloudflare-dns/api/mappings/example.com/delete', { confirm: 'example.com' }]
    ])
  })
})
