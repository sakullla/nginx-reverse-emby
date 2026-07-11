import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { unref, watch, onScopeDispose } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'
import { invalidateResourceList, useResourceListQuery } from './useResourceListQuery'

// R3: 后台异步签发期间，存在 issuing 证书时智能轮询；全部离开 issuing 停止。
const ISSUING_POLL_INTERVAL_MS = 4000

function attachIssuingPoll(query) {
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
      // Polling failures here are network-transient during the seconds-level issuing
      // refresh; vue-query's own error state stays observable to consumers, so we do
      // not raise a toast (that would be noisy). We still record the rejection for
      // diagnostics instead of silently swallowing it.
      query.refetch().catch((err) => {
        console.debug('[cert] issuing poll refresh failed', err)
      })
    }, ISSUING_POLL_INTERVAL_MS)
  }

  watch(
    () => {
      const data = query.data.value
      const list = Array.isArray(data)
        ? data
        : Array.isArray(data?.items)
          ? data.items
          : null
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

/** @deprecated Prefer useCertificatesList for list pages; kept for per-agent consumers. */
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
  return attachIssuingPoll(query)
}

/**
 * Paginated certificates list (T1 /certificates?page=...).
 * @param {{ agentFilter?: any, page?: any, pageSize?: any, q?: any, enabled?: any }} options
 */
export function useCertificatesList(options = {}) {
  const query = useResourceListQuery({
    resourceKey: 'certificates',
    agentFilter: options.agentFilter,
    page: options.page,
    pageSize: options.pageSize,
    q: options.q,
    enabled: options.enabled,
    fetcher: (params) => api.fetchCertificatesPage(params)
  })
  return attachIssuingPoll(query)
}

function invalidateCertificates(qc) {
  invalidateResourceList(qc, 'certificates')
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

export function useCreateCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload = {}) => {
      const { agentId: payloadAgentId, agent_id, ...body } = payload || {}
      const id = resolveMutationAgent(agentId, { agentId: payloadAgentId, agent_id })
      if (!id) return Promise.reject(missingAgentError())
      return api.createCertificate(id, body)
    },
    onSuccess: () => {
      invalidateCertificates(qc)
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
    mutationFn: ({ id, agentId: payloadAgentId, agent_id, ...payload }) => {
      const target = resolveMutationAgent(agentId, { agentId: payloadAgentId, agent_id })
      if (!target) return Promise.reject(missingAgentError())
      return api.updateCertificate(target, id, payload)
    },
    onSuccess: () => {
      invalidateCertificates(qc)
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
    mutationFn: (input) => {
      const certId = input && typeof input === 'object' ? input.id : input
      const target = resolveMutationAgent(agentId, input)
      if (!target) return Promise.reject(missingAgentError())
      return api.deleteCertificate(target, certId)
    },
    onSuccess: () => {
      invalidateCertificates(qc)
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
    mutationFn: (input) => {
      const certId = input && typeof input === 'object' ? input.id : input
      const target = resolveMutationAgent(agentId, input)
      if (!target) return Promise.reject(missingAgentError())
      return api.issueCertificate(target, certId)
    },
    onSuccess: () => {
      invalidateCertificates(qc)
      messageStore.success('证书签发申请已提交')
    },
    onError: (error) => {
      messageStore.error(error, '证书签发失败')
    }
  })
}
