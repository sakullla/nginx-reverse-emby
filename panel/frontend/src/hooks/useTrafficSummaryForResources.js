import { computed, unref } from 'vue'
import { useQueries, useQuery } from '@tanstack/vue-query'
import { fetchTrafficSummary } from '../api'
import { summaryBucketForObject } from '../utils/trafficStats.js'

/**
 * Shared traffic summary lookup for resource list pages.
 *
 * - Concrete agentId: one summary fetch (previous behavior).
 * - Blank / all-agents view: fetch summaries for distinct agent_id values on the
 *   current page items so card TrafficBar still renders.
 */
export function useTrafficSummaryForResources({
  agentId,
  items,
  trafficStatsEnabled,
  mapName,
  refetchInterval = 10_000,
} = {}) {
  const statsOn = computed(() => unref(trafficStatsEnabled) !== false)

  const singleAgentId = computed(() => {
    const id = unref(agentId)
    if (id == null || id === '') return null
    return String(id)
  })

  const itemAgentIds = computed(() => {
    if (singleAgentId.value) return []
    const list = unref(items) || []
    const ids = new Set()
    for (const item of list) {
      const aid = item?.agent_id
      if (aid != null && String(aid).trim()) ids.add(String(aid))
    }
    return [...ids].sort()
  })

  const singleEnabled = computed(() => Boolean(singleAgentId.value) && statsOn.value)
  const multiEnabled = computed(
    () => !singleAgentId.value && itemAgentIds.value.length > 0 && statsOn.value,
  )

  const singleQuery = useQuery({
    queryKey: computed(() => ['traffic-summary', singleAgentId.value]),
    queryFn: () => {
      const id = singleAgentId.value
      if (!id) return null
      return fetchTrafficSummary(id)
    },
    enabled: () => singleEnabled.value,
    refetchInterval,
  })

  const multiQueries = useQueries({
    queries: computed(() => {
      if (!statsOn.value || singleAgentId.value) return []
      return itemAgentIds.value.map((id) => ({
        queryKey: ['traffic-summary', id],
        queryFn: () => fetchTrafficSummary(id),
        refetchInterval,
      }))
    }),
  })

  const summaryByAgent = computed(() => {
    const map = new Map()
    if (singleAgentId.value) {
      if (singleQuery.data.value) map.set(singleAgentId.value, singleQuery.data.value)
      return map
    }
    const results = multiQueries.value || []
    itemAgentIds.value.forEach((id, index) => {
      const data = results[index]?.data
      if (data) map.set(id, data)
    })
    return map
  })

  function resolveAgentId(resource) {
    if (singleAgentId.value) return singleAgentId.value
    const aid = resource?.agent_id
    return aid != null && String(aid).trim() ? String(aid) : null
  }

  function trafficFor(resource) {
    if (!statsOn.value) return null
    const aid = resolveAgentId(resource)
    if (!aid) return null
    if (singleAgentId.value && !singleEnabled.value) return null
    if (!singleAgentId.value && !multiEnabled.value) return null
    return summaryBucketForObject(summaryByAgent.value.get(aid), mapName, resource?.id)
  }

  function nodeTotalFor(resource) {
    if (!statsOn.value) return 0
    const aid = resolveAgentId(resource)
    if (!aid) return 0
    return Number(summaryByAgent.value.get(aid)?.used_bytes) || 0
  }

  // Single-agent convenience (0 under all-agents; prefer nodeTotalFor there).
  const agentNodeTotal = computed(() => {
    if (!singleAgentId.value) return 0
    return Number(singleQuery.data.value?.used_bytes) || 0
  })

  return {
    trafficSummaryData: singleQuery.data,
    agentNodeTotal,
    nodeTotalFor,
    trafficFor,
  }
}
