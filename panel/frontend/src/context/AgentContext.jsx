import { provide, inject, ref } from 'vue'
import { isAllAgentsFilter, normalizeAgentFilter } from '../utils/agentFilter.js'

const AgentContextKey = Symbol('AgentContext')

export function AgentProvider({ children }) {
  const selectedAgentId = ref(normalizeAgentFilter(localStorage.getItem('selected_agent_id')) || 'local')

  function selectAgent(id) {
    const next = normalizeAgentFilter(id)
    selectedAgentId.value = next
    if (next) {
      localStorage.setItem('selected_agent_id', next)
    } else {
      localStorage.removeItem('selected_agent_id')
    }
  }

  provide(AgentContextKey, { selectedAgentId, selectAgent, isAllAgentsFilter })

  return children
}

export function useAgent() {
  const ctx = inject(AgentContextKey)
  if (!ctx) throw new Error('useAgent must be used within AgentProvider')
  return ctx
}