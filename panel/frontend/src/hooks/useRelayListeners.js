import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'
import { invalidateResourceList, useResourceListQuery } from './useResourceListQuery'

/** @deprecated Prefer useRelayListenersList for list pages; kept for per-agent consumers. */
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

/** Cross-agent flat list used by forms; not the paginated list-page path. */
export function useAllRelayListeners() {
  return useQuery({
    queryKey: ['relayListeners', 'all'],
    queryFn: () => api.fetchAllRelayListeners()
  })
}

/**
 * Paginated relay listeners list (T1 /relay-listeners).
 * @param {{ agentFilter?: any, page?: any, pageSize?: any, q?: any, enabled?: any }} options
 */
export function useRelayListenersList(options = {}) {
  return useResourceListQuery({
    resourceKey: 'relayListeners',
    agentFilter: options.agentFilter,
    page: options.page,
    pageSize: options.pageSize,
    q: options.q,
    enabled: options.enabled,
    fetcher: (params) => api.fetchRelayListenersPage(params)
  })
}

function invalidateRelayListeners(qc) {
  invalidateResourceList(qc, 'relayListeners')
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

export function useCreateRelayListener(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload = {}) => {
      const { agentId: payloadAgentId, agent_id, ...body } = payload || {}
      const id = resolveMutationAgent(agentId, { agentId: payloadAgentId, agent_id })
      if (!id) return Promise.reject(missingAgentError())
      return api.createRelayListener(id, body)
    },
    onSuccess: () => {
      invalidateRelayListeners(qc)
      messageStore.success('Relay 监听器创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建 Relay 监听器失败')
    }
  })
}

export function useUpdateRelayListener(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, agentId: payloadAgentId, agent_id, ...payload }) => {
      const target = resolveMutationAgent(agentId, { agentId: payloadAgentId, agent_id })
      if (!target) return Promise.reject(missingAgentError())
      return api.updateRelayListener(target, id, payload)
    },
    onSuccess: () => {
      invalidateRelayListeners(qc)
      messageStore.success('Relay 监听器更新成功')
    },
    onError: (error) => {
      messageStore.error(error, '更新 Relay 监听器失败')
    }
  })
}

export function useDeleteRelayListener(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input) => {
      const listenerId = input && typeof input === 'object' ? input.id : input
      const target = resolveMutationAgent(agentId, input)
      if (!target) return Promise.reject(missingAgentError())
      return api.deleteRelayListener(target, listenerId)
    },
    onSuccess: () => {
      invalidateRelayListeners(qc)
      messageStore.success('Relay 监听器已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 Relay 监听器失败')
    }
  })
}
