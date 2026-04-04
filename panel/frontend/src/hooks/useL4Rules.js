import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'

export function useL4Rules(agentId) {
  return useQuery({
    queryKey: ['l4Rules', agentId],
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return []
      return api.fetchL4Rules(id)
    }
  })
}

export function useCreateL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createL4Rule(unref(agentId), payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}

export function useUpdateL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateL4Rule(unref(agentId), id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}

export function useDeleteL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteL4Rule(unref(agentId), id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}
