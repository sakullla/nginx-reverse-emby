import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { ref } from 'vue'

// Shared reactive token ref — AgentContext updates it on login/logout
const _hasToken = ref(false)
export function setTokenState(token) {
  _hasToken.value = !!token
}

export function useAgents() {
  return useQuery({
    queryKey: ['agents'],
    queryFn: api.fetchAgents,
    refetchInterval: () => _hasToken.value ? 10_000 : false,
    enabled: _hasToken,
  })
}

export function useDeleteAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (agentId) => api.deleteAgent(agentId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })
  })
}

export function useRenameAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ agentId, name }) => api.renameAgent(agentId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })
  })
}
