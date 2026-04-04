import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'

export function useAgents() {
  return useQuery({
    queryKey: ['agents'],
    queryFn: api.fetchAgents,
    refetchInterval: 10_000
  })
}

export function useCreateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createAgent(payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })
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
