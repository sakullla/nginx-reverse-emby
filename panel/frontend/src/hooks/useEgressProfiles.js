import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { messageStore } from '../stores/messages'

function invalidateEgressProfiles(qc) {
  qc.invalidateQueries({ queryKey: ['egress-profiles'] })
  qc.invalidateQueries({ queryKey: ['agents'] })
}

export function useEgressProfiles() {
  return useQuery({
    queryKey: ['egress-profiles'],
    queryFn: () => api.fetchEgressProfiles()
  })
}

export function useCreateEgressProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createEgressProfile(payload),
    onSuccess: () => {
      invalidateEgressProfiles(qc)
      messageStore.success('Egress Profile 创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建 Egress Profile 失败')
    }
  })
}

export function useUpdateEgressProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateEgressProfile(id, payload),
    onSuccess: () => {
      invalidateEgressProfiles(qc)
      messageStore.success('Egress Profile 更新成功')
    },
    onError: (error) => {
      messageStore.error(error, '更新 Egress Profile 失败')
    }
  })
}

export function useDeleteEgressProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteEgressProfile(id),
    onSuccess: () => {
      invalidateEgressProfiles(qc)
      messageStore.success('Egress Profile 已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 Egress Profile 失败')
    }
  })
}
