import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { messageStore } from '../stores/messages'

export function useClientPackages() {
  return useQuery({
    queryKey: ['clientPackages'],
    queryFn: () => api.fetchClientPackages()
  })
}

export function useCreateClientPackage() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createClientPackage(payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['clientPackages'] })
      messageStore.success('客户端发布包创建成功')
    },
    onError: (error) => messageStore.error(error, '创建客户端发布包失败')
  })
}

export function useUpdateClientPackage() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateClientPackage(id, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['clientPackages'] })
      messageStore.success('客户端发布包更新成功')
    },
    onError: (error) => messageStore.error(error, '更新客户端发布包失败')
  })
}

export function useDeleteClientPackage() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteClientPackage(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['clientPackages'] })
      messageStore.success('客户端发布包已删除')
    },
    onError: (error) => messageStore.error(error, '删除客户端发布包失败')
  })
}
