import { defineComponent, h, provide, inject, ref } from 'vue'

const AgentContextKey = Symbol('AgentContext')

export const AgentProvider = defineComponent({
  name: 'AgentProvider',
  setup(props, { slots }) {
    const selectedAgentId = ref(localStorage.getItem('selected_agent_id') || 'local')

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
