import { useQuery } from '@tanstack/vue-query'
import * as api from '../api'
import { ref, watch } from 'vue'
import { computed } from 'vue'

export function useGlobalSearch(query) {
  const debouncedQuery = ref('')
  let timer = null

  watch(query, (val) => {
    clearTimeout(timer)
    timer = setTimeout(() => { debouncedQuery.value = val }, 400)
  })

  return useQuery({
    queryKey: ['globalSearch', debouncedQuery],
    queryFn: () => {
      if (!query.value || query.value.length === 0) return []
      return api.fetchAllAgentsRules([]).then((results) => {
        // Flatten and filter by query across all agents
        const q = query.value.toLowerCase()
        return results.flatMap(({ agentId, agentName, rules = [], l4Rules = [], certificates = [] }) => {
          const matchedRules = rules.filter(r =>
            r.frontend_url?.toLowerCase().includes(q) ||
            r.backend_url?.toLowerCase().includes(q) ||
            (r.tags || []).some(t => t.toLowerCase().includes(q))
          )
          const matchedL4 = (l4Rules || []).filter(r =>
            (r.name || '').toLowerCase().includes(q) ||
            String(r.listen_port || '').includes(q) ||
            (r.tags || []).some(t => t.toLowerCase().includes(q))
          )
          const matchedCerts = (certificates || []).filter(c =>
            c.domain?.toLowerCase().includes(q) ||
            (c.tags || []).some(t => t.toLowerCase().includes(q))
          )
          if (matchedRules.length || matchedL4.length || matchedCerts.length) {
            return [{ agentId, agentName, rules: matchedRules, l4Rules: matchedL4, certificates: matchedCerts }]
          }
          return []
        }).flat()
      })
    },
    enabled: computed(() => debouncedQuery.value.length > 0)
  })
}
