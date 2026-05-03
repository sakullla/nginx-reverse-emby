import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import * as api from '../api'
import { messageStore } from '../stores/messages'

function trafficTrendKey(agentId, params = {}) {
  const id = unref(agentId)
  const value = unref(params) || {}
  const granularity = unref(value.granularity) || 'day'
  const range = unref(value.range) || [
    unref(value.from) || '',
    unref(value.to) || ''
  ]
  const scope = unref(value.scope) || [
    unref(value.scope_type) || '',
    unref(value.scope_id) || ''
  ]
  return ['traffic-trend', id, granularity, range, scope]
}

function trafficTrendParams(params = {}) {
  const value = unref(params) || {}
  const range = unref(value.range)
  const scope = unref(value.scope)
  return {
    granularity: unref(value.granularity) || 'day',
    from: Array.isArray(range) ? range[0] : unref(value.from),
    to: Array.isArray(range) ? range[1] : unref(value.to),
    scope_type: Array.isArray(scope) ? scope[0] : unref(value.scope_type),
    scope_id: Array.isArray(scope) ? scope[1] : unref(value.scope_id)
  }
}

function invalidateTraffic(qc, agentId) {
  const id = unref(agentId)
  qc.invalidateQueries({ queryKey: ['traffic-policy', id] })
  qc.invalidateQueries({ queryKey: ['traffic-summary', id] })
  qc.invalidateQueries({ queryKey: ['traffic-trend', id] })
  qc.invalidateQueries({ queryKey: ['agents'] })
}

export function useTrafficPolicy(agentId) {
  return useQuery({
    queryKey: computed(() => ['traffic-policy', unref(agentId)]),
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return null
      return api.fetchTrafficPolicy(id)
    },
    enabled: () => !!unref(agentId)
  })
}

export function useTrafficSummary(agentId) {
  return useQuery({
    queryKey: computed(() => ['traffic-summary', unref(agentId)]),
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return null
      return api.fetchTrafficSummary(id)
    },
    enabled: () => !!unref(agentId)
  })
}

export function useTrafficTrend(agentId, params) {
  return useQuery({
    queryKey: computed(() => trafficTrendKey(agentId, params)),
    queryFn: () => {
      const id = unref(agentId)
      if (!id) return []
      return api.fetchTrafficTrend(id, trafficTrendParams(params))
    },
    enabled: () => !!unref(agentId)
  })
}

export function useUpdateTrafficPolicy(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (patch) => api.updateTrafficPolicy(unref(agentId), patch),
    onSuccess: () => {
      invalidateTraffic(qc, agentId)
      messageStore.success('流量策略已更新')
    },
    onError: (error) => {
      messageStore.error(error, '更新流量策略失败')
    }
  })
}

export function useCalibrateTraffic(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.calibrateTraffic(unref(agentId), payload),
    onSuccess: () => {
      invalidateTraffic(qc, agentId)
      messageStore.success('流量统计已校准')
    },
    onError: (error) => {
      messageStore.error(error, '校准流量统计失败')
    }
  })
}

export function useCleanupTraffic(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.cleanupTraffic(unref(agentId)),
    onSuccess: () => {
      invalidateTraffic(qc, agentId)
      messageStore.success('流量历史已清理')
    },
    onError: (error) => {
      messageStore.error(error, '清理流量历史失败')
    }
  })
}
