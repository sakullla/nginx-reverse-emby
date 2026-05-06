import { defineComponent, h, provide, inject, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useAgents } from '../hooks/useAgents'
import { fetchSystemInfo } from '../api'
import { useAuthState } from './useAuthState'
import { reconcileSelectedAgent } from './agentSelection.js'

const AgentContextKey = Symbol('AgentContext')

export const AgentProvider = defineComponent({
  name: 'AgentProvider',
  setup(props, { slots }) {
    const savedId = localStorage.getItem('selected_agent_id')
    const selectedAgentId = ref(savedId || null)
    const route = useRoute()

    // Sync URL query agentId into persistent context so sidebar navigation
    // (which uses static paths without query params) preserves the selection.
    watch(() => route.query.agentId, (id) => {
      if (id && id !== selectedAgentId.value) {
        selectedAgentId.value = id
        localStorage.setItem('selected_agent_id', id)
      }
    }, { immediate: true })

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
        } catch (err) {
          console.error('[AgentContext] fetchSystemInfo failed', err)
        }
        systemInfoAttempted.value = true
      }
    }, { immediate: true })

    watch([agentsData, systemInfo, systemInfoAttempted], ([agents, info, attempted]) => {
      const next = reconcileSelectedAgent({
        currentSelectedAgentId: selectedAgentId.value,
        agents,
        systemInfo: info,
        systemInfoAttempted: attempted
      })

      if (next.clear) {
        selectedAgentId.value = null
        localStorage.removeItem('selected_agent_id')
        return
      }

      if (next.nextSelectedAgentId !== selectedAgentId.value) {
        selectedAgentId.value = next.nextSelectedAgentId
      }
      if (next.persist && next.nextSelectedAgentId) {
        localStorage.setItem('selected_agent_id', next.nextSelectedAgentId)
      }
    })

    function selectAgent(id) {
      selectedAgentId.value = id
      localStorage.setItem('selected_agent_id', id)
      recordAgentUsage(id)
    }

    const RECENT_AGENTS_KEY = 'nre_recent_agent_ids'
    const MAX_RECENT_AGENTS = 20

    function recordAgentUsage(id) {
      if (!id) return
      try {
        const raw = localStorage.getItem(RECENT_AGENTS_KEY)
        const list = raw ? JSON.parse(raw) : []
        const filtered = list.filter(item => item !== id)
        filtered.unshift(id)
        const trimmed = filtered.slice(0, MAX_RECENT_AGENTS)
        localStorage.setItem(RECENT_AGENTS_KEY, JSON.stringify(trimmed))
      } catch {
        localStorage.setItem(RECENT_AGENTS_KEY, JSON.stringify([id]))
      }
    }

    provide(AgentContextKey, { selectedAgentId, selectAgent, recordAgentUsage, systemInfo })

    return () => slots.default?.()
  }
})

export function useAgent() {
  const ctx = inject(AgentContextKey)
  if (!ctx) throw new Error('useAgent must be used within AgentProvider')
  return ctx
}
