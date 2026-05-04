import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { computed, defineComponent, nextTick, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { useTrafficTrend } from '../../hooks/useTraffic.js'
import * as api from '../../api'

vi.mock('../../api', () => ({
  fetchTrafficTrend: vi.fn(async () => [])
}))

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false }
    }
  })
}

describe('useTrafficTrend', () => {
  it('refetches trend data when range params change', async () => {
    const range = ref(['2026-05-01T00:00:00Z', '2026-05-02T00:00:00Z'])
    const Harness = defineComponent({
      setup() {
        useTrafficTrend(
          ref('edge-1'),
          computed(() => ({
            granularity: 'day',
            range: range.value
          }))
        )
        return () => null
      }
    })

    mount(Harness, {
      global: {
        plugins: [[VueQueryPlugin, { queryClient: createQueryClient() }]]
      }
    })
    await nextTick()
    await vi.dynamicImportSettled()
    expect(api.fetchTrafficTrend).toHaveBeenCalledWith('edge-1', expect.objectContaining({
      from: '2026-05-01T00:00:00Z',
      to: '2026-05-02T00:00:00Z'
    }))

    range.value = ['2026-05-03T00:00:00Z', '2026-05-04T00:00:00Z']
    await nextTick()
    await vi.dynamicImportSettled()

    expect(api.fetchTrafficTrend).toHaveBeenCalledWith('edge-1', expect.objectContaining({
      from: '2026-05-03T00:00:00Z',
      to: '2026-05-04T00:00:00Z'
    }))
    expect(api.fetchTrafficTrend).toHaveBeenCalledTimes(2)
  })
})
