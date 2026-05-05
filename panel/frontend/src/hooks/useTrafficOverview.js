import { useQuery } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import { fetchTrafficOverview } from '../api'

export function useTrafficOverview(agentId, enabled = true, granularity = 'day') {
  return useQuery({
    queryKey: computed(() => ['traffic-overview', unref(agentId) || 'all', unref(granularity)]),
    queryFn: () => fetchTrafficOverview(unref(agentId) || null, unref(granularity)),
    enabled: computed(() => Boolean(unref(enabled))),
    refetchInterval: 30_000
  })
}
