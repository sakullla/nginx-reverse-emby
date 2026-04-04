import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'

export function useRules(agentId) {
  return useQuery({
    queryKey: ['rules', agentId],
    queryFn: () => {
      if (!agentId.value) return []
      return api.fetchRules(agentId.value)
    }
  })
}

export function useCreateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createRule(agentId.value, payload.frontend_url, payload.backend_url, payload.tags, payload.enabled, payload.proxy_redirect),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules', agentId] })
  })
}

export function useUpdateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateRule(agentId.value, id, payload.frontend_url, payload.backend_url, payload.tags, payload.enabled, payload.proxy_redirect),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules', agentId] })
  })
}

export function useDeleteRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (ruleId) => api.deleteRule(agentId.value, ruleId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules', agentId] })
  })
}
