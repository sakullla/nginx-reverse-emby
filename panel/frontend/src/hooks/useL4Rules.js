import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'

export function useL4Rules(agentId) {
  return useQuery({
    queryKey: ['l4Rules', agentId],
    queryFn: () => {
      if (!agentId.value) return []
      return api.fetchL4Rules(agentId.value)
    }
  })
}

export function useCreateL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createL4Rule(agentId.value, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}

export function useUpdateL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateL4Rule(agentId.value, id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}

export function useDeleteL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteL4Rule(agentId.value, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}
