import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'

function invalidateWireGuardReferences(qc, agentId) {
  qc.invalidateQueries({ queryKey: ['wireGuardProfiles', agentId] })
  qc.invalidateQueries({ queryKey: ['wireGuardClients', agentId] })
  qc.invalidateQueries({ queryKey: ['agents'] })
  qc.invalidateQueries({ queryKey: ['relayListeners', agentId] })
  qc.invalidateQueries({ queryKey: ['relayListeners', 'all'] })
  qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
}

function invalidateWireGuardClientTarget(qc, rawAgentId, rawProfileId) {
  qc.invalidateQueries({ queryKey: ['wireGuardClients', rawAgentId, rawProfileId] })
  invalidateWireGuardReferences(qc, rawAgentId)
}

export function useWireGuardProfiles(agentId) {
  return useQuery({
    queryKey: ['wireGuardProfiles', agentId],
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return []
      return api.fetchWireGuardProfiles(id)
    }
  })
}

export function useCreateWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createWireGuardProfile(unref(agentId), payload),
    onSuccess: () => {
      invalidateWireGuardReferences(qc, agentId)
      messageStore.success('WireGuard Profile 创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建 WireGuard Profile 失败')
    }
  })
}

export function useUpdateWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateWireGuardProfile(unref(agentId), id, payload),
    onSuccess: () => {
      invalidateWireGuardReferences(qc, agentId)
      messageStore.success('WireGuard Profile 更新成功')
    },
    onError: (error) => {
      messageStore.error(error, '更新 WireGuard Profile 失败')
    }
  })
}

export function useDeleteWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteWireGuardProfile(unref(agentId), id),
    onSuccess: () => {
      invalidateWireGuardReferences(qc, agentId)
      messageStore.success('WireGuard Profile 已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 WireGuard Profile 失败')
    }
  })
}

export function useWireGuardClients(agentId, profileId) {
  return useQuery({
    queryKey: ['wireGuardClients', agentId, profileId],
    queryFn: () => {
      const id = unref(agentId)
      const profile = unref(profileId)
      if (!id || !profile) return []
      return api.fetchWireGuardClients(id, profile)
    },
    enabled: computedEnabled(agentId, profileId)
  })
}

export function useCreateWireGuardClient(agentId, profileId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => {
      const rawAgentId = unref(agentId)
      const rawProfileId = unref(profileId)
      return api.createWireGuardClient(rawAgentId, rawProfileId, payload)
    },
    onMutate: () => ({
      rawAgentId: unref(agentId),
      rawProfileId: unref(profileId)
    }),
    onSuccess: (_client, _payload, context) => {
      const rawAgentId = context?.rawAgentId
      const rawProfileId = context?.rawProfileId
      invalidateWireGuardClientTarget(qc, rawAgentId, rawProfileId)
      messageStore.success('WireGuard Client 创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建 WireGuard Client 失败')
    }
  })
}

export function useUpdateWireGuardClient(agentId, profileId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ clientId, ...payload }) => {
      const rawAgentId = unref(agentId)
      const rawProfileId = unref(profileId)
      const rawClientId = clientId
      return api.updateWireGuardClient(rawAgentId, rawProfileId, rawClientId, payload)
    },
    onMutate: ({ clientId }) => ({
      rawAgentId: unref(agentId),
      rawProfileId: unref(profileId),
      rawClientId: clientId
    }),
    onSuccess: (_client, _payload, context) => {
      const rawAgentId = context?.rawAgentId
      const rawProfileId = context?.rawProfileId
      invalidateWireGuardClientTarget(qc, rawAgentId, rawProfileId)
      messageStore.success('WireGuard Client 已更新')
    },
    onError: (error) => {
      messageStore.error(error, '更新 WireGuard Client 失败')
    }
  })
}

export function useDeleteWireGuardClient(agentId, profileId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (clientId) => {
      const rawAgentId = unref(agentId)
      const rawProfileId = unref(profileId)
      const rawClientId = clientId
      return api.deleteWireGuardClient(rawAgentId, rawProfileId, rawClientId)
    },
    onMutate: (clientId) => ({
      rawAgentId: unref(agentId),
      rawProfileId: unref(profileId),
      rawClientId: clientId
    }),
    onSuccess: (_client, _clientId, context) => {
      const rawAgentId = context?.rawAgentId
      const rawProfileId = context?.rawProfileId
      invalidateWireGuardClientTarget(qc, rawAgentId, rawProfileId)
      messageStore.success('WireGuard Client 已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 WireGuard Client 失败')
    }
  })
}

function computedEnabled(agentId, profileId) {
  return computed(() => Boolean(unref(agentId) && unref(profileId)))
}
