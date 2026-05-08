import { useQuery, keepPreviousData } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import { fetchTrafficAggregate } from '../api'

export function useTrafficAggregate(agentId, enabled = true, granularity = 'day') {
  return useQuery({
    queryKey: computed(() => ['traffic-aggregate', unref(agentId) || 'all', unref(granularity)]),
    queryFn: () => fetchTrafficAggregate(unref(agentId) || null, unref(granularity)),
    enabled: computed(() => Boolean(unref(enabled))),
    refetchInterval: 30_000,
    placeholderData: keepPreviousData
  })
}
