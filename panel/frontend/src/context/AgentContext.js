import { defineComponent, h, provide, inject, ref, watch } from 'vue'
import { useAgents } from '../hooks/useAgents'

const AgentContextKey = Symbol('AgentContext')

export const AgentProvider = defineComponent({
  name: 'AgentProvider',
  setup(props, { slots }) {
    const savedId = localStorage.getItem('selected_agent_id')
    const selectedAgentId = ref(savedId || 'local')

    // useAgents is owned here so we can validate whenever the agents list updates
    const { data: agentsData } = useAgents()

    // Validate and update selectedAgentId whenever agents list changes
    watch(agentsData, (agents) => {
      if (!agents || agents.length === 0) return
      const ids = new Set(agents.map(a => a.id))
      if (!ids.has(selectedAgentId.value)) {
        // Persisted ID is stale — fall back to 'local' or first available
        const defaultId = agents.find(a => a.id === 'local')?.id
          || agents[0]?.id
          || 'local'
        selectedAgentId.value = defaultId
        localStorage.setItem('selected_agent_id', defaultId)
      }
    }, { immediate: true })

    function selectAgent(id) {
      selectedAgentId.value = id
      localStorage.setItem('selected_agent_id', id)
    }

    provide(AgentContextKey, { selectedAgentId, selectAgent })

    return () => slots.default?.()
  }
})

export function useAgent() {
  const ctx = inject(AgentContextKey)
  if (!ctx) throw new Error('useAgent must be used within AgentProvider')
  return ctx
}
