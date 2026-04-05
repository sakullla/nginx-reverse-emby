import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'

export function useRules(agentId) {
  return useQuery({
    queryKey: ['rules', agentId],
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return []
      return api.fetchRules(id)
    }
  })
}

export function useCreateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createRule(
      unref(agentId),
      payload.frontend_url,
      payload.backend_url,
      payload.tags,
      payload.enabled,
      payload.proxy_redirect,
      payload.pass_proxy_headers,
      payload.user_agent,
      payload.custom_headers
    ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rules', agentId] })
      qc.invalidateQueries({ queryKey: ['agents'] })
    }
  })
}

export function useUpdateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateRule(
      unref(agentId),
      id,
      payload.frontend_url,
      payload.backend_url,
      payload.tags,
      payload.enabled,
      payload.proxy_redirect,
      payload.pass_proxy_headers,
      payload.user_agent,
      payload.custom_headers
    ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rules', agentId] })
      qc.invalidateQueries({ queryKey: ['agents'] })
    }
  })
}

export function useDeleteRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (ruleId) => api.deleteRule(unref(agentId), ruleId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rules', agentId] })
      qc.invalidateQueries({ queryKey: ['agents'] })
    }
  })
}
