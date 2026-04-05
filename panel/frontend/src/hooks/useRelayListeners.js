import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'

export function useRelayListeners(agentId) {
  return useQuery({
    queryKey: ['relayListeners', agentId],
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return []
      return api.fetchRelayListeners(id)
    }
  })
}

export function useCreateRelayListener(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createRelayListener(unref(agentId), payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['relayListeners', agentId] })
      qc.invalidateQueries({ queryKey: ['agents'] })
    }
  })
}

export function useUpdateRelayListener(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateRelayListener(unref(agentId), id, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['relayListeners', agentId] })
      qc.invalidateQueries({ queryKey: ['agents'] })
    }
  })
}

export function useDeleteRelayListener(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteRelayListener(unref(agentId), id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['relayListeners', agentId] })
      qc.invalidateQueries({ queryKey: ['agents'] })
    }
  })
}
