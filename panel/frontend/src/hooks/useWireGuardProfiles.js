import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'
import { invalidateResourceList, useResourceListQuery } from './useResourceListQuery'

function invalidateWireGuardReferences(qc, agentId) {
  invalidateResourceList(qc, 'wireGuardProfiles')
  qc.invalidateQueries({ queryKey: ['wireGuardClients', agentId] })
  qc.invalidateQueries({ queryKey: ['agents'] })
  invalidateResourceList(qc, 'relayListeners')
  invalidateResourceList(qc, 'l4Rules')
}

function invalidateWireGuardClientTarget(qc, rawAgentId, rawProfileId) {
  qc.invalidateQueries({ queryKey: ['wireGuardClients', rawAgentId, rawProfileId] })
  invalidateWireGuardReferences(qc, rawAgentId)
}

/** @deprecated Prefer useWireGuardProfilesList for list pages; kept for per-agent consumers. */
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

/**
 * Paginated WireGuard profiles list (/wireguard-profiles).
 * @param {{ agentFilter?: any, page?: any, pageSize?: any, q?: any, enabledFilter?: any, status?: any, enabled?: any }} options
 */
export function useWireGuardProfilesList(options = {}) {
  return useResourceListQuery({
    resourceKey: 'wireGuardProfiles',
    agentFilter: options.agentFilter,
    page: options.page,
    pageSize: options.pageSize,
    q: options.q,
    enabledFilter: options.enabledFilter,
    status: options.status,
    enabled: options.enabled,
    fetcher: (params) => api.fetchWireGuardProfilesPage(params)
  })
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

export function useCreateWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload = {}) => {
      const { agentId: payloadAgentId, agent_id, ...body } = payload || {}
      const id = resolveMutationAgent(agentId, { agentId: payloadAgentId, agent_id })
      if (!id) return Promise.reject(missingAgentError())
      return api.createWireGuardProfile(id, body)
    },
    onSuccess: (_data, variables) => {
      const id = resolveMutationAgent(agentId, variables)
      invalidateWireGuardReferences(qc, id || agentId)
      messageStore.success('WireGuard 配置 创建成功')
    },
    onError: (error) => {
      messageStore.error(error, '创建 WireGuard 配置 失败')
    }
  })
}

export function useUpdateWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, agentId: payloadAgentId, agent_id, ...payload }) => {
      const target = resolveMutationAgent(agentId, { agentId: payloadAgentId, agent_id })
      if (!target) return Promise.reject(missingAgentError())
      return api.updateWireGuardProfile(target, id, payload)
    },
    onSuccess: (_data, variables) => {
      const id = resolveMutationAgent(agentId, variables)
      invalidateWireGuardReferences(qc, id || agentId)
      messageStore.success('WireGuard 配置 更新成功')
    },
    onError: (error) => {
      messageStore.error(error, '更新 WireGuard 配置 失败')
    }
  })
}

export function useDeleteWireGuardProfile(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input) => {
      const profileId = input && typeof input === 'object' ? input.id : input
      const target = resolveMutationAgent(agentId, input)
      if (!target) return Promise.reject(missingAgentError())
      return api.deleteWireGuardProfile(target, profileId)
    },
    onSuccess: (_data, variables) => {
      const id = resolveMutationAgent(agentId, variables)
      invalidateWireGuardReferences(qc, id || agentId)
      messageStore.success('WireGuard 配置 已删除')
    },
    onError: (error) => {
      messageStore.error(error, '删除 WireGuard 配置 失败')
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
