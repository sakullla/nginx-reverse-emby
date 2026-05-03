import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { useAuthState } from '../context/useAuthState'
import { messageStore } from '../stores/messages'

export function useAgents() {
  const { hasToken } = useAuthState()
  return useQuery({
    queryKey: ['agents'],
    queryFn: api.fetchAgents,
    refetchInterval: () => hasToken.value ? 10_000 : false,
    enabled: () => !!hasToken.value,
  })
}

export function useDeleteAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (agentId) => api.deleteAgent(agentId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['agents'] })
      messageStore.success('节点已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除节点失败')
    }
  })
}

export function useRenameAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ agentId, name }) => api.renameAgent(agentId, name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['agents'] })
      messageStore.success('节点名称已更新')
    },
    onError: (error) => {
      messageStore.error(error, '重命名节点失败')
    }
  })
}

export function useUpdateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ agentId, payload }) => api.updateAgent(agentId, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['agents'] })
      qc.invalidateQueries({ queryKey: ['agent-stats'] })
      messageStore.success('节点设置已更新')
    },
    onError: (error) => {
      messageStore.error(error, '更新节点设置失败')
    }
  })
}
