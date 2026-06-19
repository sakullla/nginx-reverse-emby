import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { unref, watch, onScopeDispose } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'

// R3: 后台异步签发期间，存在 issuing 证书时智能轮询；全部离开 issuing 停止。
const ISSUING_POLL_INTERVAL_MS = 4000

export function useCertificates(agentId) {
  const query = useQuery({
    queryKey: ['certificates', agentId],
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return []
      return api.fetchCertificates(id)
    },
    refetchOnWindowFocus: true,
  })

  let pollTimer = null
  function stopPolling() {
    if (pollTimer !== null) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }
  function startPolling() {
    if (pollTimer !== null) return
    pollTimer = setInterval(() => {
      query.refetch().catch(() => {})
    }, ISSUING_POLL_INTERVAL_MS)
  }

  watch(
    () => {
      const list = query.data.value
      if (!Array.isArray(list)) return false
      return list.some((cert) => cert && cert.status === 'issuing')
    },
    (hasIssuing) => {
      if (hasIssuing) startPolling()
      else stopPolling()
    },
    { immediate: true }
  )

  onScopeDispose(stopPolling)

  return query
}

export function useCreateCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createCertificate(unref(agentId), payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['certificates', agentId] })
      messageStore.success('证书已创建，签发任务已提交')
    },
    onError: (error) => {
      messageStore.error(error, '创建证书失败')
    }
  })
}

export function useUpdateCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateCertificate(unref(agentId), id, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['certificates', agentId] })
      messageStore.success('证书已更新，变更已提交')
    },
    onError: (error) => {
      messageStore.error(error, '更新证书失败')
    }
  })
}

export function useDeleteCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteCertificate(unref(agentId), id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['certificates', agentId] })
      messageStore.success('证书已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除证书失败')
    }
  })
}

export function useIssueCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.issueCertificate(unref(agentId), id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['certificates', agentId] })
      messageStore.success('证书签发申请已提交')
    },
    onError: (error) => {
      messageStore.error(error, '证书签发失败')
    }
  })
}
