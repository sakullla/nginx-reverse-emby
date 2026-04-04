import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { computed } from 'vue'

export function useRules(agentId) {
  return useQuery({
    queryKey: ['rules', agentId],
    queryFn: () => api.fetchRules(agentId.value),
    enabled: computed(() => !!agentId.value)
  })
}

export function useCreateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createRule(agentId.value, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules', agentId] })
  })
}

export function useUpdateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateRule(agentId.value, id, payload),
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
