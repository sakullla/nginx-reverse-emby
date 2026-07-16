// @vitest-environment node

import { describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'

const useQueryMock = vi.fn((options) => options)

vi.mock('@tanstack/vue-query', () => ({
  useQuery: (options) => useQueryMock(options)
}))

import { ALL_AGENTS_FILTER } from '../utils/agentFilter.js'
import { useResourceListQuery } from './useResourceListQuery.js'

describe('useResourceListQuery', () => {
  it('builds queryKey with agent filter, page, pageSize, q, enabledFilter, and status', () => {
    const scope = effectScope(true)
    let options
    scope.run(() => {
      options = useResourceListQuery({
        resourceKey: 'rules',
        agentFilter: ref('edge'),
        page: ref(2),
        pageSize: ref(50),
        q: ref('app'),
        enabledFilter: ref(true),
        status: ref('active'),
        fetcher: vi.fn()
      })
    })
    expect(options.queryKey.value).toEqual(['rules', 'edge', 2, 50, 'app', true, 'active'])
    scope.stop()
  })

  it('passes concrete agentId to fetcher and omits agent for all filter', async () => {
    const fetcher = vi.fn(async () => ({ items: [], total: 0, page: 1, page_size: 20 }))
    const scope = effectScope(true)
    let options
    scope.run(() => {
      options = useResourceListQuery({
        resourceKey: 'rules',
        agentFilter: ref(ALL_AGENTS_FILTER),
        page: 1,
        pageSize: 20,
        q: '',
        fetcher
      })
    })
    await options.queryFn()
    expect(fetcher).toHaveBeenCalledWith({
      agentId: undefined,
      page: 1,
      pageSize: 20,
      q: '',
      enabled: undefined,
      status: undefined
    })
    scope.stop()

    const scope2 = effectScope(true)
    scope2.run(() => {
      options = useResourceListQuery({
        resourceKey: 'l4Rules',
        agentFilter: 'local',
        page: 3,
        pageSize: 10,
        q: 'tcp',
        fetcher
      })
    })
    await options.queryFn()
    expect(fetcher).toHaveBeenLastCalledWith({
      agentId: 'local',
      page: 3,
      pageSize: 10,
      q: 'tcp',
      enabled: undefined,
      status: undefined
    })
    scope2.stop()
  })

  it('forwards enabledFilter/status to fetcher and queryKey', async () => {
    const fetcher = vi.fn(async () => ({ items: [], total: 0, page: 1, page_size: 20 }))
    const enabledFilter = ref(false)
    const status = ref('pending')
    const scope = effectScope(true)
    let options
    scope.run(() => {
      options = useResourceListQuery({
        resourceKey: 'certificates',
        agentFilter: ref(ALL_AGENTS_FILTER),
        page: 1,
        pageSize: 20,
        enabledFilter,
        status,
        fetcher
      })
    })
    expect(options.queryKey.value).toEqual(['certificates', ALL_AGENTS_FILTER, 1, 20, '', false, 'pending'])
    await options.queryFn()
    expect(fetcher).toHaveBeenCalledWith({
      agentId: undefined,
      page: 1,
      pageSize: 20,
      q: '',
      enabled: false,
      status: 'pending'
    })
    enabledFilter.value = true
    status.value = 'active'
    await nextTick()
    expect(options.queryKey.value[5]).toBe(true)
    expect(options.queryKey.value[6]).toBe('active')
    scope.stop()
  })

  it('reacts when agentFilter changes from concrete to all', async () => {
    const agentFilter = ref('local')
    const fetcher = vi.fn(async () => ({ items: [], total: 0, page: 1, page_size: 20 }))
    const scope = effectScope(true)
    let options
    scope.run(() => {
      options = useResourceListQuery({
        resourceKey: 'certificates',
        agentFilter,
        page: 1,
        pageSize: 20,
        fetcher
      })
    })
    expect(options.queryKey.value[1]).toBe('local')
    agentFilter.value = ALL_AGENTS_FILTER
    await nextTick()
    expect(options.queryKey.value[1]).toBe(ALL_AGENTS_FILTER)
    await options.queryFn()
    expect(fetcher).toHaveBeenCalledWith(expect.objectContaining({ agentId: undefined }))
    scope.stop()
  })
})
