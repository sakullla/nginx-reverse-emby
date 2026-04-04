import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'

export function useCertificates() {
  return useQuery({
    queryKey: ['certificates'],
    queryFn: api.fetchCertificates
  })
}

export function useCreateCertificate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createCertificate(payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates'] })
  })
}

export function useUpdateCertificate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateCertificate(id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates'] })
  })
}

export function useDeleteCertificate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteCertificate(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates'] })
  })
}

export function useIssueCertificate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.issueCertificate(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates'] })
  })
}
