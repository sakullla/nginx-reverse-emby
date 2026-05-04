import { useQuery } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import { fetchTrafficOverview } from '../api'

export function useTrafficOverview(agentId) {
  return useQuery({
    queryKey: computed(() => ['traffic-overview', unref(agentId) || 'all']),
    queryFn: () => fetchTrafficOverview(unref(agentId) || null),
    refetchInterval: 30_000
  })
}
