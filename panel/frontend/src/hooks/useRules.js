import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'
import { invalidateResourceList, useResourceListQuery } from './useResourceListQuery'
export { useDiagnoseRule } from './useDiagnostics'

/** @deprecated Prefer useRulesList for list pages; kept for per-agent consumers. */
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

/**
 * Paginated HTTP rules list (T1 /http-rules).
 * @param {{ agentFilter?: any, page?: any, pageSize?: any, q?: any, enabled?: any }} options
 */
export function useRulesList(options = {}) {
  return useResourceListQuery({
    resourceKey: 'rules',
    agentFilter: options.agentFilter,
    page: options.page,
    pageSize: options.pageSize,
    q: options.q,
    enabled: options.enabled,
    fetcher: (params) => api.fetchHttpRulesPage(params)
  })
}

function invalidateRules(qc) {
  invalidateResourceList(qc, 'rules')
  qc.invalidateQueries({ queryKey: ['agents'] })
}

export function useCreateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createRule(unref(agentId), payload),
    onSuccess: () => {
      invalidateRules(qc)
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
      invalidateRules(qc)
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
      invalidateRules(qc)
      messageStore.success('HTTP 规则已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除规则失败')
    }
  })
}
