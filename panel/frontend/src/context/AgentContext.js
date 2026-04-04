import { defineComponent, h, provide, inject, ref, watch } from 'vue'
import { useAgents } from '../hooks/useAgents'
import { fetchSystemInfo } from '../api'
import { useAuthState } from './useAuthState'

const AgentContextKey = Symbol('AgentContext')

export const AgentProvider = defineComponent({
  name: 'AgentProvider',
  setup(props, { slots }) {
    const savedId = localStorage.getItem('selected_agent_id')
    const selectedAgentId = ref(savedId || null)
    // Track whether the current selection was made by the user (not auto-chosen)
    const userChosen = ref(savedId !== null)

    // useAgents is owned here so we can validate whenever the agents list updates
    const { data: agentsData } = useAgents()

    // Track token reactively so login changes are picked up without remounting
    const { token: tokenVal, setToken } = useAuthState()
    const systemInfo = ref(null)

    // Re-read token whenever storage changes (login stores token, logout removes it)
    watch(tokenVal, async (token) => {
      systemInfo.value = null
      if (token) {
        try {
          systemInfo.value = await fetchSystemInfo()
        } catch {}
      }
    }, { immediate: true })

    // Validate selectedAgentId whenever agents list or systemInfo loads.
    // Only auto-select when BOTH datasets are available and the user has not made
    // an explicit choice (userChosen === false). This ensures systemInfo's
    // default_agent_id is respected on fresh logins even if agents arrive first.
    watch([agentsData, systemInfo], ([agents, info]) => {
      if (!agents || agents.length === 0) return

      const ids = new Set(agents.map(a => a.id))
      const currentValid = selectedAgentId.value && ids.has(selectedAgentId.value)

      if (userChosen.value) return        // don't override user choice
      if (currentValid) return             // already have a valid selection

      // Neither dataset was sufficient before — now that both may be here, pick a default
      if (!agents || !agents.length) return

      // Wait for systemInfo before first auto-selection so backend default is honoured
      if (!info && !userChosen.value) return

      const defaultId = info?.default_agent_id
        || agents.find(a => a.id === 'local')?.id
        || agents[0]?.id
      selectedAgentId.value = defaultId
      localStorage.setItem('selected_agent_id', defaultId)
    })

    function selectAgent(id) {
      selectedAgentId.value = id
      userChosen.value = true
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
