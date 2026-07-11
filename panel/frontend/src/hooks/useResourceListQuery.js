import { useQuery } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import { isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'

/**
 * Shared vue-query wrapper for control-plane paginated list endpoints.
 *
 * queryKey shape: [resourceKey, agentFilter|null|'__all__', page, pageSize, q]
 * - concrete agent id → backend agent_id
 * - all / blank → omit agent_id (all agents)
 */
export function useResourceListQuery({
  resourceKey,
  agentFilter,
  page = 1,
  pageSize = 20,
  q = '',
  fetcher,
  enabled
} = {}) {
  if (!resourceKey) {
    throw new Error('useResourceListQuery requires resourceKey')
  }
  if (typeof fetcher !== 'function') {
    throw new Error('useResourceListQuery requires fetcher')
  }

  const queryKey = computed(() => {
    const filter = normalizeAgentFilter(unref(agentFilter))
    const pageNum = Number(unref(page))
    const sizeNum = Number(unref(pageSize))
    const query = unref(q)
    return [
      resourceKey,
      filter,
      Number.isInteger(pageNum) && pageNum > 0 ? pageNum : 1,
      Number.isInteger(sizeNum) && sizeNum > 0 ? sizeNum : 20,
      query == null ? '' : String(query).trim()
    ]
  })

  return useQuery({
    queryKey,
    enabled: computed(() => {
      if (enabled === undefined) return true
      return Boolean(unref(enabled))
    }),
    queryFn: () => {
      const filter = normalizeAgentFilter(unref(agentFilter))
      const pageNum = Number(unref(page))
      const sizeNum = Number(unref(pageSize))
      const query = unref(q)
      return fetcher({
        agentId: isAllAgentsFilter(filter) ? undefined : (filter || undefined),
        page: Number.isInteger(pageNum) && pageNum > 0 ? pageNum : 1,
        pageSize: Number.isInteger(sizeNum) && sizeNum > 0 ? sizeNum : 20,
        q: query == null ? '' : String(query).trim()
      })
    },
    placeholderData: (previousData) => previousData
  })
}

/** Invalidate every cache entry under a resource list key prefix. */
export function invalidateResourceList(qc, resourceKey) {
  if (!qc || !resourceKey) return
  qc.invalidateQueries({ queryKey: [resourceKey] })
}
