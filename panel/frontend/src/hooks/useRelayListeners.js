import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'

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

export function useAllRelayListeners() {
  return useQuery({
    queryKey: ['relayListeners', 'all'],
    queryFn: () => api.fetchAllRelayListeners()
  })
}

export function useCreateRelayListener(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createRelayListener(unref(agentId), payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['relayListeners', agentId] })
      qc.invalidateQueries({ queryKey: ['relayListeners', 'all'] })
      qc.invalidateQueries({ queryKey: ['agents'] })
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
      qc.invalidateQueries({ queryKey: ['relayListeners', agentId] })
      qc.invalidateQueries({ queryKey: ['relayListeners', 'all'] })
      qc.invalidateQueries({ queryKey: ['agents'] })
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
      qc.invalidateQueries({ queryKey: ['relayListeners', agentId] })
      qc.invalidateQueries({ queryKey: ['relayListeners', 'all'] })
      qc.invalidateQueries({ queryKey: ['agents'] })
      messageStore.success('Relay 监听器已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 Relay 监听器失败')
    }
  })
}
