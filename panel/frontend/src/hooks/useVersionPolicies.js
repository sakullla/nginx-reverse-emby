import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { messageStore } from '../stores/messages'

export function useVersionPolicies() {
  return useQuery({
    queryKey: ['versionPolicies'],
    queryFn: () => api.fetchVersionPolicies()
  })
}

export function useCreateVersionPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createVersionPolicy(payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['versionPolicies'] })
      messageStore.success('版本策略创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建版本策略失败')
    }
  })
}

export function useUpdateVersionPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateVersionPolicy(id, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['versionPolicies'] })
      messageStore.success('版本策略更新成功')
    },
    onError: (error) => {
      messageStore.error(error, '更新版本策略失败')
    }
  })
}

export function useDeleteVersionPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteVersionPolicy(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['versionPolicies'] })
      messageStore.success('版本策略已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除版本策略失败')
    }
  })
}
