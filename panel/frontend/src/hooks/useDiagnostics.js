import { computed, unref } from 'vue'
import { useMutation, useQuery } from '@tanstack/vue-query'
import * as api from '../api'
import { messageStore } from '../stores/messages'

const TERMINAL_STATES = new Set(['completed', 'failed'])

export function useDiagnosticTask(agentId, taskId) {
  const currentAgentId = computed(() => unref(agentId))
  const currentTaskId = computed(() => unref(taskId))

  return useQuery({
    queryKey: ['diagnosticTask', currentAgentId, currentTaskId],
    enabled: computed(() => Boolean(currentAgentId.value && currentTaskId.value)),
    refetchInterval: (query) => {
      const state = query.state.data?.task?.state
      return state && TERMINAL_STATES.has(state) ? false : 1200
    },
    queryFn: () => api.fetchAgentTask(currentAgentId.value, currentTaskId.value)
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

export function useDiagnoseRule(agentId) {
  return useMutation({
    mutationFn: (input) => {
      const ruleId = input && typeof input === 'object' ? input.id : input
      const target = resolveMutationAgent(agentId, input)
      if (!target) return Promise.reject(missingAgentError())
      return api.diagnoseRule(target, ruleId)
    },
    onError: (error) => {
      messageStore.error(error, '启动 HTTP 诊断失败')
    }
  })
}

export function useDiagnoseL4Rule(agentId) {
  return useMutation({
    mutationFn: (input) => {
      const ruleId = input && typeof input === 'object' ? input.id : input
      const target = resolveMutationAgent(agentId, input)
      if (!target) return Promise.reject(missingAgentError())
      return api.diagnoseL4Rule(target, ruleId)
    },
    onError: (error) => {
      messageStore.error(error, '启动 L4 诊断失败')
    }
  })
}

export function diagnosticStateLabel(state) {
  return {
    pending: '等待派发',
    dispatched: '已派发',
    running: '诊断中',
    completed: '已完成',
    failed: '失败'
  }[state] || '处理中'
}

export function diagnosticStateTone(state) {
  return {
    completed: 'success',
    failed: 'danger',
    pending: 'muted',
    dispatched: 'info',
    running: 'info'
  }[state] || 'muted'
}
