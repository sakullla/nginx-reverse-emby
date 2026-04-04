import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'

export function useCertificates(agentId) {
  return useQuery({
    queryKey: ['certificates', agentId],
    queryFn: () => {
      if (!agentId.value) return []
      return api.fetchCertificates(agentId.value)
    }
  })
}

export function useCreateCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createCertificate(agentId.value, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates', agentId] })
  })
}

export function useUpdateCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateCertificate(agentId.value, id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates', agentId] })
  })
}

export function useDeleteCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteCertificate(agentId.value, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates', agentId] })
  })
}

export function useIssueCertificate(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.issueCertificate(agentId.value, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates', agentId] })
  })
}
