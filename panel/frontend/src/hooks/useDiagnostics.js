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

export function useDiagnoseRule(agentId) {
  return useMutation({
    mutationFn: (ruleId) => api.diagnoseRule(unref(agentId), ruleId),
    onError: (error) => {
      messageStore.error(error, '启动 HTTP 诊断失败')
    }
  })
}

export function useDiagnoseL4Rule(agentId) {
  return useMutation({
    mutationFn: (ruleId) => api.diagnoseL4Rule(unref(agentId), ruleId),
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
