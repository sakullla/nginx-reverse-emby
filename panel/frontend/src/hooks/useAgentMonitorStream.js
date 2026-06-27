import { useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, unref, watch } from 'vue'
import * as api from '../api'
import { useAuthState } from '../context/useAuthState'
import { mergeMonitorAgents, monitorSnapshotAgents } from '../utils/agentMonitor'

export const AGENT_MONITOR_QUERY_KEY = ['agent-monitor']
export const AGENT_MONITOR_RECONNECT_DELAY_MS = 2000

function mergeAgentList(previous, monitorAgent) {
  if (!monitorAgent?.id || !Array.isArray(previous)) return previous
  return previous.map((agent) => agent?.id === monitorAgent.id
    ? {
        ...agent,
        name: monitorAgent.name || agent.name,
        status: monitorAgent.status || agent.status,
        last_seen_at: monitorAgent.last_seen_at || agent.last_seen_at,
        last_seen_ip: monitorAgent.last_seen_ip || agent.last_seen_ip,
        version: monitorAgent.version || agent.version,
        platform: monitorAgent.platform || agent.platform,
        mode: monitorAgent.mode || agent.mode,
        tags: Array.isArray(monitorAgent.tags) ? monitorAgent.tags : agent.tags,
        monitor: monitorAgent
      }
    : agent
  )
}

export function applyAgentMonitorMessage(queryClient, message) {
  if (!message || !queryClient) return
  if (message.type === 'snapshot') {
    const agents = monitorSnapshotAgents(message.payload)
    queryClient.setQueryData(AGENT_MONITOR_QUERY_KEY, agents)
    agents.forEach((agent) => {
      queryClient.setQueryData(['agents'], (previous) => mergeAgentList(previous, agent))
    })
    return
  }
  if (message.type === 'update') {
    const agent = message.payload?.agent
    queryClient.setQueryData(AGENT_MONITOR_QUERY_KEY, (previous) => mergeMonitorAgents(previous, message.payload))
    queryClient.setQueryData(['agents'], (previous) => mergeAgentList(previous, agent))
  }
}

export function useAgentMonitorStream(options = {}) {
  const queryClient = useQueryClient()
  const { hasToken } = useAuthState()
  const enabled = computed(() => {
    if (options.enabled == null) return hasToken.value
    return !!unref(options.enabled) && hasToken.value
  })
  const data = ref(queryClient.getQueryData(AGENT_MONITOR_QUERY_KEY) || [])
  const status = ref('idle')
  const error = ref(null)
  const reconnectDelay = options.reconnectDelay ?? AGENT_MONITOR_RECONNECT_DELAY_MS
  let controller = null
  let reconnectTimer = null
  let stopped = false

  function clearReconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  function stopStream() {
    clearReconnect()
    if (controller) {
      controller.abort()
      controller = null
    }
  }

  function scheduleReconnect() {
    if (stopped || !enabled.value || reconnectDelay < 0) return
    clearReconnect()
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      startStream()
    }, reconnectDelay)
  }

  async function startStream() {
    if (stopped || !enabled.value || controller) return
    const currentController = new AbortController()
    controller = currentController
    status.value = 'connecting'
    error.value = null
    try {
      await api.consumeAgentMonitorStream({
        signal: currentController.signal,
        onMessage: (message) => {
          status.value = 'connected'
          applyAgentMonitorMessage(queryClient, message)
          data.value = queryClient.getQueryData(AGENT_MONITOR_QUERY_KEY) || []
        }
      })
      if (!currentController.signal.aborted && controller === currentController) {
        status.value = 'disconnected'
        controller = null
        scheduleReconnect()
      }
    } catch (err) {
      if (!currentController.signal.aborted && controller === currentController) {
        status.value = 'error'
        error.value = err
        controller = null
        scheduleReconnect()
      }
    }
  }

  watch(enabled, (next) => {
    if (next) {
      startStream()
      return
    }
    stopStream()
    status.value = 'idle'
  }, { immediate: true })

  onScopeDispose(() => {
    stopped = true
    stopStream()
  })

  return {
    data: computed(() => data.value),
    status,
    error,
    reconnect: startStream,
    stop: stopStream
  }
}
