import { createContext, useContext, ref } from 'vue'

const AgentContext = createContext(null)

export function AgentProvider({ children }) {
  // Default to 'local' agent
  const selectedAgentId = ref(localStorage.getItem('selected_agent_id') || 'local')

  function selectAgent(id) {
    selectedAgentId.value = id
    localStorage.setItem('selected_agent_id', id)
  }

  return (
    <AgentContext.Provider value={{ selectedAgentId, selectAgent }}>
      {children}
    </AgentContext.Provider>
  )
}

export function useAgent() {
  const ctx = useContext(AgentContext)
  if (!ctx) throw new Error('useAgent must be used within AgentProvider')
  return ctx
}
