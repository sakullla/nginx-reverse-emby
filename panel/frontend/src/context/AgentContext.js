import { defineComponent, h, provide, inject, ref, watch } from 'vue'
import { useAgents, setTokenState } from '../hooks/useAgents'
import { fetchSystemInfo } from '../api'

const AgentContextKey = Symbol('AgentContext')

export const AgentProvider = defineComponent({
  name: 'AgentProvider',
  setup(props, { slots }) {
    const savedId = localStorage.getItem('selected_agent_id')
    // Initialize to null — only set to a real agent id once agents + systemInfo have loaded
    const selectedAgentId = ref(savedId || null)

    // useAgents is owned here so we can validate whenever the agents list updates
    const { data: agentsData } = useAgents()

    // Track token reactively so login changes are picked up without remounting
    const tokenVal = ref(localStorage.getItem('panel_token'))
    const systemInfo = ref(null)

    // Re-read token whenever storage changes (login stores token, logout removes it)
    watch(tokenVal, async (token) => {
      setTokenState(token)
      systemInfo.value = null
      if (token) {
        try {
          systemInfo.value = await fetchSystemInfo()
        } catch {}
      }
    }, { immediate: true })

    // Validate selectedAgentId whenever agents list or systemInfo loads
    watch([agentsData, systemInfo], ([agents, info]) => {
      if (!agents || agents.length === 0) return

      const ids = new Set(agents.map(a => a.id))

      // If current selection is null or not in the agent list, select a valid default
      if (selectedAgentId.value === null || !ids.has(selectedAgentId.value)) {
        const defaultId = info?.default_agent_id
          || agents.find(a => a.id === 'local')?.id
          || agents[0]?.id
        selectedAgentId.value = defaultId
        localStorage.setItem('selected_agent_id', defaultId)
      }
    })

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
