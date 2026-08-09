import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
const post = vi.fn()
const patch = vi.fn()
const del = vi.fn()
vi.mock('./client', () => ({ api: { get, post, patch, delete: del } }))

const {
  fetchRepositorySources,
  fetchRepositorySource,
  createRepositorySource,
  updateRepositorySource,
  deleteRepositorySource,
  refreshRepositorySource,
  fetchRepositoryEntries,
  fetchRepositoryContents
} = await import('./pluginRepositories.js')

describe('plugin repository source API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    patch.mockReset()
    del.mockReset()
  })

  it('uses the stable marketplace source endpoints and envelopes', async () => {
    const source = { id: 'plugins', purpose: 'plugin' }
    get.mockResolvedValueOnce({ data: { sources: [source] } })
    get.mockResolvedValueOnce({ data: { source } })
    post.mockResolvedValueOnce({ data: { source } })
    patch.mockResolvedValueOnce({ data: { source: { ...source, ref_name: 'v1' } } })
    post.mockResolvedValueOnce({ data: { snapshot: { source_id: 'plugins', commit: 'a'.repeat(40) } } })
    get.mockResolvedValueOnce({ data: { entries: [{ id: 'waf' }] } })
    get.mockResolvedValueOnce({ data: { entries: [], direct_plugin: { id: 'direct-waf' } } })
    del.mockResolvedValueOnce({ data: { ok: true } })

    await expect(fetchRepositorySources()).resolves.toEqual([source])
    await expect(fetchRepositorySource('plugins')).resolves.toEqual(source)
    await createRepositorySource(source)
    await updateRepositorySource('plugins', { ref_name: 'v1' })
    await refreshRepositorySource('plugins')
    await expect(fetchRepositoryEntries('plugins')).resolves.toEqual([{ id: 'waf' }])
    await expect(fetchRepositoryContents('plugins')).resolves.toEqual({ entries: [], directPlugin: { id: 'direct-waf' } })
    await expect(deleteRepositorySource('plugins')).resolves.toEqual({ ok: true })

    expect(get).toHaveBeenNthCalledWith(1, '/marketplace/sources')
    expect(get).toHaveBeenNthCalledWith(2, '/marketplace/sources/plugins')
    expect(post).toHaveBeenNthCalledWith(1, '/marketplace/sources', source)
    expect(patch).toHaveBeenCalledWith('/marketplace/sources/plugins', { ref_name: 'v1' })
    expect(post).toHaveBeenNthCalledWith(2, '/marketplace/sources/plugins/refresh')
    expect(get).toHaveBeenNthCalledWith(3, '/marketplace/sources/plugins/entries')
    expect(get).toHaveBeenNthCalledWith(4, '/marketplace/sources/plugins/entries')
    expect(del).toHaveBeenCalledWith('/marketplace/sources/plugins')
  })

  it('encodes source ids and rejects an empty identity', async () => {
    get.mockResolvedValue({ data: { source: {} } })
    await fetchRepositorySource('team/plugins')
    expect(get).toHaveBeenCalledWith('/marketplace/sources/team%2Fplugins')
    await expect(fetchRepositorySource(' ')).rejects.toThrow('repository source id is required')
  })
})
