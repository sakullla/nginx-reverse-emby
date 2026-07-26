import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, reactive } from 'vue'
import { useListFilterUrl } from '../../composables/useListFilterUrl.js'
import { buildListQueryParams } from '../../api/runtime.js'

function createHarness({ query = {}, schema, debounceMs = 300 } = {}) {
  const route = reactive({ query: { ...query } })
  const router = { replace: vi.fn() }
  let values
  let setValue
  const wrapper = mount(defineComponent({
    setup() {
      const result = useListFilterUrl({ route, router, schema, debounceMs })
      values = result.values
      setValue = result.setValue
      return () => h('div')
    }
  }))
  activeWrappers.push(wrapper)
  return { route, router, values, setValue }
}

const activeWrappers = []

const SCHEMA = {
  search: { key: 'search', type: 'string', baseline: '' },
  enabled: { key: 'enabled', type: 'enum', values: ['1', '0'], baseline: '' },
  sync: { key: 'sync', type: 'enum', values: ['applied', 'pending'], baseline: '' },
  tags: { key: 'tags', type: 'list', baseline: [] },
  agentId: { key: 'agentId', type: 'string', baseline: '', validate: (v) => ['1', '2'].includes(v) }
}

describe('useListFilterUrl', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    while (activeWrappers.length) {
      activeWrappers.pop().unmount()
    }
    vi.clearAllTimers()
    vi.useRealTimers()
  })

  it('initializes values from the URL query', () => {
    const { values } = createHarness({
      query: { search: 'emby', enabled: '1', tags: 'a,b' },
      schema: SCHEMA
    })
    expect(values.search).toBe('emby')
    expect(values.enabled).toBe('1')
    expect(values.tags).toEqual(['a', 'b'])
  })

  it('falls back to baseline for invalid enum values', () => {
    const { values } = createHarness({ query: { enabled: '999', sync: 'applied' }, schema: SCHEMA })
    expect(values.enabled).toBe('')
    expect(values.sync).toBe('applied')
  })

  it('silently drops values rejected by a custom validator', () => {
    const { values } = createHarness({ query: { agentId: '42' }, schema: SCHEMA })
    expect(values.agentId).toBe('')
  })

  it('parses list values from array query params and filters blanks', () => {
    const { values } = createHarness({ query: { tags: ['a', ' ', 'b'] }, schema: SCHEMA })
    expect(values.tags).toEqual(['a', 'b'])
  })

  it('writes state changes to the URL after the debounce, omitting baselines', async () => {
    const { route, router, setValue } = createHarness({ schema: SCHEMA })
    setValue('enabled', '1')
    setValue('tags', ['emby', 'web'])
    expect(router.replace).not.toHaveBeenCalled()

    vi.advanceTimersByTime(300)
    expect(router.replace).toHaveBeenCalledTimes(1)
    expect(router.replace).toHaveBeenCalledWith({
      query: { enabled: '1', tags: 'emby,web' }
    })
    expect(route.query).not.toHaveProperty('sync')
  })

  it('coalesces rapid updates into a single replace', () => {
    const { router, setValue } = createHarness({ schema: SCHEMA })
    setValue('search', 'e')
    vi.advanceTimersByTime(100)
    setValue('search', 'em')
    vi.advanceTimersByTime(100)
    setValue('search', 'emb')
    vi.advanceTimersByTime(300)
    expect(router.replace).toHaveBeenCalledTimes(1)
    expect(router.replace).toHaveBeenCalledWith({ query: { search: 'emb' } })
  })

  it('strips keys that return to baseline and preserves foreign keys', () => {
    const { router, setValue } = createHarness({ query: { enabled: '1', search: '#id=7' }, schema: SCHEMA })
    setValue('enabled', '')
    vi.advanceTimersByTime(300)
    expect(router.replace).toHaveBeenCalledWith({ query: { search: '#id=7' } })
  })

  it('flushes immediately when opts.immediate is set', () => {
    const { router, setValue } = createHarness({ schema: SCHEMA })
    setValue('sync', 'pending', { immediate: true })
    expect(router.replace).toHaveBeenCalledTimes(1)
    expect(router.replace).toHaveBeenCalledWith({ query: { sync: 'pending' } })
  })

  it('reflects external URL changes back into state (round-trip)', async () => {
    const { route, values, setValue } = createHarness({ schema: SCHEMA })
    setValue('tags', ['emby'])
    vi.advanceTimersByTime(300)

    // simulate the router applying the new query (deep-link / shared URL load)
    route.query = { tags: 'emby', enabled: '0' }
    await nextTick()
    expect(values.tags).toEqual(['emby'])
    expect(values.enabled).toBe('0')
  })
})

describe('buildListQueryParams filter dimensions', () => {
  it('omits new filter params when unset', () => {
    expect(buildListQueryParams({ page: 1 })).toEqual({ page: 1, page_size: 20 })
  })

  it('serializes tags as comma-joined and ids as strings', () => {
    const params = buildListQueryParams({
      tags: ['emby', 'web'],
      certificateId: 7,
      egressProfileId: '3',
      relayListenerId: 11
    })
    expect(params.tags).toBe('emby,web')
    expect(params.certificate_id).toBe('7')
    expect(params.egress_profile_id).toBe('3')
    expect(params.relay_listener_id).toBe('11')
  })

  it('passes sync and referenced only when meaningful', () => {
    expect(buildListQueryParams({ sync: 'applied' }).sync).toBe('applied')
    expect(buildListQueryParams({ sync: '' })).not.toHaveProperty('sync')
    expect(buildListQueryParams({ referenced: true }).referenced).toBe(true)
    expect(buildListQueryParams({})).not.toHaveProperty('referenced')
  })

  it('drops empty tag lists and non-positive ids', () => {
    const params = buildListQueryParams({ tags: [], certificateId: 0, egressProfileId: 'x' })
    expect(params).not.toHaveProperty('tags')
    expect(params).not.toHaveProperty('certificate_id')
    expect(params).not.toHaveProperty('egress_profile_id')
  })
})
