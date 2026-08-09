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
 * Paginated HTTP rules list (/http-rules).
 * @param {{ agentFilter?: any, page?: any, pageSize?: any, q?: any, enabledFilter?: any, status?: any, filters?: any, enabled?: any }} options
 */
export function useRulesList(options = {}) {
  return useResourceListQuery({
    resourceKey: 'rules',
    agentFilter: options.agentFilter,
    page: options.page,
    pageSize: options.pageSize,
    q: options.q,
    enabledFilter: options.enabledFilter,
    status: options.status,
    filters: options.filters,
    enabled: options.enabled,
    fetcher: (params) => api.fetchHttpRulesPage(params)
  })
}

function invalidateRules(qc) {
  invalidateResourceList(qc, 'rules')
  qc.invalidateQueries({ queryKey: ['agents'] })
}

function resolveMutationAgent(defaultAgentId, input) {
  if (input && typeof input === 'object') {
    const override = input.agentId ?? input.agent_id
    if (override != null && String(override).trim()) return String(override).trim()
  }
  const fallback = unref(defaultAgentId)
  if (fallback != null && String(fallback).trim()) return String(fallback).trim()
  return null
}

function missingAgentError() {
  return new Error('缺少节点归属，无法执行该操作')
}

export function formatRuleMutationError(error) {
  const message = error?.response?.data?.message || error?.message || ''
  if (message.includes('master_cf_dns certificates') && message.includes('local master agent')) {
    return new Error('远程节点不能使用主控 DNS 证书，请改用“节点 HTTP-01”证书，或将规则绑定到本地主控节点。')
  }
  return error
}

export function useCreateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload = {}) => {
      const { agentId: payloadAgentId, agent_id, ...body } = payload || {}
      const id = resolveMutationAgent(agentId, { agentId: payloadAgentId, agent_id })
      if (!id) return Promise.reject(missingAgentError())
      return api.createRule(id, body)
    },
    onSuccess: () => {
      invalidateRules(qc)
      messageStore.success('HTTP 规则创建成功')
    },
    onError: (error) => {
      messageStore.error(formatRuleMutationError(error), '创建规则失败')
    }
  })
}

export function useUpdateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, agentId: payloadAgentId, agent_id, ...payload }) => {
      const target = resolveMutationAgent(agentId, { agentId: payloadAgentId, agent_id })
      if (!target) return Promise.reject(missingAgentError())
      return api.updateRule(target, id, payload)
    },
    onSuccess: () => {
      invalidateRules(qc)
      messageStore.success('HTTP 规则更新成功')
    },
    onError: (error) => {
      messageStore.error(formatRuleMutationError(error), '更新规则失败')
    }
  })
}

export function useDeleteRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input) => {
      const ruleId = input && typeof input === 'object' ? input.id : input
      const target = resolveMutationAgent(agentId, input)
      if (!target) return Promise.reject(missingAgentError())
      return api.deleteRule(target, ruleId)
    },
    onSuccess: () => {
      invalidateRules(qc)
      messageStore.success('HTTP 规则已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除规则失败')
    }
  })
}
