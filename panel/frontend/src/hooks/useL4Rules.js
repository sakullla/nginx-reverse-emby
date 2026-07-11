import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'
import { invalidateResourceList, useResourceListQuery } from './useResourceListQuery'
export { useDiagnoseL4Rule } from './useDiagnostics'

/** @deprecated Prefer useL4RulesList for list pages; kept for per-agent consumers. */
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

/**
 * Paginated L4 rules list (T1 /l4-rules).
 * @param {{ agentFilter?: any, page?: any, pageSize?: any, q?: any, enabled?: any }} options
 */
export function useL4RulesList(options = {}) {
  return useResourceListQuery({
    resourceKey: 'l4Rules',
    agentFilter: options.agentFilter,
    page: options.page,
    pageSize: options.pageSize,
    q: options.q,
    enabled: options.enabled,
    fetcher: (params) => api.fetchL4RulesPage(params)
  })
}

function invalidateL4Rules(qc) {
  invalidateResourceList(qc, 'l4Rules')
  qc.invalidateQueries({ queryKey: ['agents'] })
}

export function useCreateL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createL4Rule(unref(agentId), payload),
    onSuccess: () => {
      invalidateL4Rules(qc)
      messageStore.success('L4 规则创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建 L4 规则失败')
    }
  })
}

export function useUpdateL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateL4Rule(unref(agentId), id, payload),
    onSuccess: () => {
      invalidateL4Rules(qc)
      messageStore.success('L4 规则更新成功')
    },
    onError: (error) => {
      messageStore.error(error, '更新 L4 规则失败')
    }
  })
}

export function useDeleteL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteL4Rule(unref(agentId), id),
    onSuccess: () => {
      invalidateL4Rules(qc)
      messageStore.success('L4 规则已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 L4 规则失败')
    }
  })
}
