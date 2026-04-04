import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { unref } from 'vue'
import * as api from '../api'

export function useCertificates(agentId) {
  return useQuery({
    queryKey: ['certificates', agentId],
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return []
      return api.fetchCertificates(id)
    }
  })
}

export function useCreateCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createCertificate(unref(agentId), payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates', agentId] })
  })
}

export function useUpdateCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateCertificate(unref(agentId), id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates', agentId] })
  })
}

export function useDeleteCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteCertificate(unref(agentId), id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates', agentId] })
  })
}

export function useIssueCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.issueCertificate(unref(agentId), id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates', agentId] })
  })
}
