import { provide, inject, ref } from 'vue'

const AgentContextKey = Symbol('AgentContext')

export function AgentProvider({ children }) {
  const selectedAgentId = ref(localStorage.getItem('selected_agent_id') || 'local')

  function selectAgent(id) {
    selectedAgentId.value = id
    localStorage.setItem('selected_agent_id', id)
  }

  provide(AgentContextKey, { selectedAgentId, selectAgent })

  return children
}

export function useAgent() {
  const ctx = inject(AgentContextKey)
  if (!ctx) throw new Error('useAgent must be used within AgentProvider')
  return ctx
}