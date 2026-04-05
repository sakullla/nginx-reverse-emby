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
    // Tracks whether fetchSystemInfo has completed (success or failure).
    // This distinguishes "still loading" from "failed" so a transient /info error
    // doesn't permanently block agent auto-selection.
    const systemInfoAttempted = ref(false)

    // Re-read token whenever storage changes (login stores token, logout removes it)
    watch(tokenVal, async (token) => {
      systemInfo.value = null
      systemInfoAttempted.value = false
      if (token) {
        try {
          systemInfo.value = await fetchSystemInfo()
        } catch {}
        systemInfoAttempted.value = true
      }
    }, { immediate: true })

    // Validate selectedAgentId whenever agents list or systemInfo loads.
    // Always check the saved ID is still valid — if the selected agent was deleted,
    // reset even a user-chosen selection to avoid querying a dead agent ID.
    // When the agent list goes empty, clear the selection so the next valid agent
    // is picked up automatically.
    watch([agentsData, systemInfo], ([agents, info]) => {
      // Clear selection when the last agent is removed
      if (!agents || agents.length === 0) {
        selectedAgentId.value = null
        userChosen.value = false
        localStorage.removeItem('selected_agent_id')
        return
      }

      const ids = new Set(agents.map(a => a.id))
      const currentValid = selectedAgentId.value && ids.has(selectedAgentId.value)

      if (currentValid) return               // selection is still valid, nothing to do

      // Selected agent is gone (deleted or never existed) — clear userChosen so we
      // can auto-select a valid default below
      userChosen.value = false

      // Only wait for systemInfo when we have no selection AND haven't attempted /info yet.
      // Once attempted (even if failed), fall back to local/first so the UI stays usable.
      if (!info && !systemInfoAttempted.value && !selectedAgentId.value) return

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

    provide(AgentContextKey, { selectedAgentId, selectAgent, systemInfo })

    return () => slots.default?.()
  }
})

export function useAgent() {
  const ctx = inject(AgentContextKey)
  if (!ctx) throw new Error('useAgent must be used within AgentProvider')
  return ctx
}
