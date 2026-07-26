import { useQuery, keepPreviousData } from '@tanstack/vue-query'
import { fetchDashboardAttention } from '../api'

export function useAttention() {
  return useQuery({
    queryKey: ['dashboard-attention'],
    queryFn: fetchDashboardAttention,
    refetchInterval: 30_000,
    placeholderData: keepPreviousData
  })
}
