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

export function useCreateRelayListener(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createRelayListener(unref(agentId), payload),
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
    mutationFn: ({ id, ...payload }) => api.updateRelayListener(unref(agentId), id, payload),
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
    mutationFn: (id) => api.deleteRelayListener(unref(agentId), id),
    onSuccess: () => {
      invalidateRelayListeners(qc)
      messageStore.success('Relay 监听器已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 Relay 监听器失败')
    }
  })
}
