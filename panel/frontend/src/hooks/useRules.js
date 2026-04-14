import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'
export { useDiagnoseRule } from './useDiagnostics'

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
    mutationFn: (payload) => api.createRule(unref(agentId), payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rules', agentId] })
      qc.invalidateQueries({ queryKey: ['agents'] })
      messageStore.success('HTTP 规则创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建规则失败')
    }
  })
}

export function useUpdateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateRule(unref(agentId), id, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['rules', agentId] })
      qc.invalidateQueries({ queryKey: ['agents'] })
      messageStore.success('HTTP 规则更新成功')
    },
    onError: (error) => {
      messageStore.error(error, '更新规则失败')
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
      messageStore.success('HTTP 规则已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除规则失败')
    }
  })
}
