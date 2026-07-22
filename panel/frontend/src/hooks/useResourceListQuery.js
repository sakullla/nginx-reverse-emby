import { useQuery } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import { isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'

function normalizeOptionalEnabledFilter(value) {
  const raw = unref(value)
  if (raw === true || raw === false) return raw
  return undefined
}

function normalizeOptionalStatusFilter(value) {
  const raw = unref(value)
  if (raw == null) return ''
  return String(raw).trim()
}

/**
 * Normalize the extended filter dimensions (tags, related-resource ids,
 * sync, referenced) into a stable object for queryKey / fetcher params.
 * Empty values are dropped so unset dimensions never reach the backend.
 */
function normalizeExtendedFilters(value) {
  const raw = unref(value)
  if (!raw || typeof raw !== 'object') return {}
  const out = {}
  for (const [key, item] of Object.entries(raw)) {
    if (item === undefined || item === null || item === '') continue
    if (Array.isArray(item)) {
      const cleaned = item.map((entry) => String(entry).trim()).filter(Boolean)
      if (cleaned.length) out[key] = [...cleaned].sort()
      continue
    }
    out[key] = item
  }
  return out
}

/**
 * Shared vue-query wrapper for control-plane paginated list endpoints.
 *
 * queryKey shape: [resourceKey, agentFilter, page, pageSize, q, enabledFilter, status]
 * - concrete agent id → backend agent_id
 * - all / blank → omit agent_id (all agents)
 * - enabledFilter boolean → backend enabled; undefined omits
 * - status non-empty → backend status; empty omits
 * - filters object → extended dimensions (tags/certificateId/egressProfileId/
 *   relayListenerId/sync/referenced), forwarded to buildListQueryParams
 * - `enabled` remains the vue-query enable flag (not the list filter)
 */
export function useResourceListQuery({
  resourceKey,
  agentFilter,
  page = 1,
  pageSize = 20,
  q = '',
  enabledFilter,
  status = '',
  filters,
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
    const listEnabled = normalizeOptionalEnabledFilter(enabledFilter)
    const listStatus = normalizeOptionalStatusFilter(status)
    return [
      resourceKey,
      filter,
      Number.isInteger(pageNum) && pageNum > 0 ? pageNum : 1,
      Number.isInteger(sizeNum) && sizeNum > 0 ? sizeNum : 20,
      query == null ? '' : String(query).trim(),
      listEnabled === undefined ? null : listEnabled,
      listStatus,
      normalizeExtendedFilters(filters)
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
      const listEnabled = normalizeOptionalEnabledFilter(enabledFilter)
      const listStatus = normalizeOptionalStatusFilter(status)
      return fetcher({
        agentId: isAllAgentsFilter(filter) ? undefined : (filter || undefined),
        page: Number.isInteger(pageNum) && pageNum > 0 ? pageNum : 1,
        pageSize: Number.isInteger(sizeNum) && sizeNum > 0 ? sizeNum : 20,
        q: query == null ? '' : String(query).trim(),
        enabled: listEnabled,
        status: listStatus || undefined,
        ...normalizeExtendedFilters(filters)
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
