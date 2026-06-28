import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { useGlobalSearch } from './useGlobalSearch.js'
import * as api from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    fetchAllAgentsRules: vi.fn(() => Promise.resolve([]))
  }
})

function createQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function mountHarness(queryClient, queryRef) {
  const Harness = defineComponent({
    setup() {
      useGlobalSearch(queryRef)
      return () => null
    }
  })
  return mount(Harness, {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]]
    }
  })
}

describe('useGlobalSearch', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('clears pending debounce timer on scope dispose', async () => {
    const clearTimeoutSpy = vi.spyOn(global, 'clearTimeout')
    const queryClient = createQueryClient()
    const query = ref('')
    const wrapper = mountHarness(queryClient, query)
    await nextTick()

    query.value = 'test'
    await nextTick()
    clearTimeoutSpy.mockClear()

    // Timer is pending; unmount before it fires
    wrapper.unmount()

    expect(clearTimeoutSpy).toHaveBeenCalled()
  })
})
