import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'

function invalidateWireGuardReferences(qc, agentId) {
  qc.invalidateQueries({ queryKey: ['wireGuardProfiles', agentId] })
  qc.invalidateQueries({ queryKey: ['agents'] })
  qc.invalidateQueries({ queryKey: ['relayListeners', agentId] })
  qc.invalidateQueries({ queryKey: ['relayListeners', 'all'] })
  qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
}

export function useWireGuardProfiles(agentId) {
  return useQuery({
    queryKey: ['wireGuardProfiles', agentId],
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return []
      return api.fetchWireGuardProfiles(id)
    }
  })
}

export function useCreateWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createWireGuardProfile(unref(agentId), payload),
    onSuccess: () => {
      invalidateWireGuardReferences(qc, agentId)
      messageStore.success('WireGuard Profile 创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建 WireGuard Profile 失败')
    }
  })
}

export function useUpdateWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateWireGuardProfile(unref(agentId), id, payload),
    onSuccess: () => {
      invalidateWireGuardReferences(qc, agentId)
      messageStore.success('WireGuard Profile 更新成功')
    },
    onError: (error) => {
      messageStore.error(error, '更新 WireGuard Profile 失败')
    }
  })
}

export function useDeleteWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteWireGuardProfile(unref(agentId), id),
    onSuccess: () => {
      invalidateWireGuardReferences(qc, agentId)
      messageStore.success('WireGuard Profile 已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 WireGuard Profile 失败')
    }
  })
}
