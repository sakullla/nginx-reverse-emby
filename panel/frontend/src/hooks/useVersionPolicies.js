import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'

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
    onSuccess: () => qc.invalidateQueries({ queryKey: ['versionPolicies'] })
  })
}

export function useUpdateVersionPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateVersionPolicy(id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['versionPolicies'] })
  })
}

export function useDeleteVersionPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteVersionPolicy(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['versionPolicies'] })
  })
}
