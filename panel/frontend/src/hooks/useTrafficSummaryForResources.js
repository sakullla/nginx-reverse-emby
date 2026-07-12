import { computed, unref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { fetchTrafficSummary } from '../api'
import { summaryBucketForObject } from '../utils/trafficStats.js'

/**
 * Shared traffic summary lookup for resource list pages.
 * Fetches summary for a concrete agent and maps rows by mapName (http_rules / l4_rules / relay_listeners).
 */
export function useTrafficSummaryForResources({
  agentId,
  items,
  trafficStatsEnabled,
  mapName,
  refetchInterval = 10_000,
} = {}) {
  const enabled = computed(() => {
    const id = unref(agentId)
    const statsOn = unref(trafficStatsEnabled)
    return Boolean(id) && statsOn !== false
  })

  const { data: trafficSummaryData } = useQuery({
    queryKey: computed(() => ['traffic-summary', unref(agentId)]),
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return null
      return fetchTrafficSummary(id)
    },
    enabled: () => enabled.value,
    refetchInterval,
  })

  const agentNodeTotal = computed(() => {
    if (!enabled.value) return 0
    return Number(trafficSummaryData.value?.used_bytes) || 0
  })

  function trafficFor(resource) {
    if (!enabled.value) return null
    return summaryBucketForObject(trafficSummaryData.value, mapName, resource?.id)
  }

  // Keep items dependency explicit so callers can pass reactive lists without unused-arg lint.
  void items

  return {
    trafficSummaryData,
    agentNodeTotal,
    trafficFor,
  }
}
