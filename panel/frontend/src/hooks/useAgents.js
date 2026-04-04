import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { computed } from 'vue'

export function useAgents() {
  const hasToken = computed(() => !!localStorage.getItem('panel_token'))
  return useQuery({
    queryKey: ['agents'],
    queryFn: api.fetchAgents,
    refetchInterval: hasToken.value ? 10_000 : false,
    refetchOnWindowFocus: hasToken,
  })
}

export function useDeleteAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (agentId) => api.deleteAgent(agentId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })
  })
}

export function useRenameAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ agentId, name }) => api.renameAgent(agentId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })
  })
}
