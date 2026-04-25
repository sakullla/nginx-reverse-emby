import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getAgentStatus } from '../utils/agentHelpers.js'

const STORAGE_KEY = 'agent-list-view'

export function useAgentFilters(agentsRef) {
  const route = useRoute()
  const router = useRouter()

  // View preference (card/list) with localStorage fallback
  const view = ref(route.query.view || localStorage.getItem(STORAGE_KEY) || 'card')
  watch(view, (v) => {
    localStorage.setItem(STORAGE_KEY, v)
    syncQuery({ view: v })
  })

  // Filters
  const statusFilter = ref(route.query.status || '')
  const modeFilter = ref(route.query.mode || '')
  const tagFilter = ref(route.query.tag || '')

  // Sort
  const sortField = ref(route.query.sort || 'last_seen_at')
  const sortOrder = ref(route.query.order || 'desc')

  // Search
  const searchQuery = ref('')

  // Sync filters/sort to URL query
  function syncQuery(overrides = {}) {
    const query = {
      ...route.query,
      view: view.value,
      ...(statusFilter.value ? { status: statusFilter.value } : {}),
      ...(modeFilter.value ? { mode: modeFilter.value } : {}),
      ...(tagFilter.value ? { tag: tagFilter.value } : {}),
      sort: sortField.value,
      order: sortOrder.value,
      ...overrides
    }
    // Remove empty values
    Object.keys(query).forEach(key => {
      if (!query[key] && key !== 'sort' && key !== 'order' && key !== 'view') {
        delete query[key]
      }
    })
    router.replace({ query })
  }

  watch([statusFilter, modeFilter, tagFilter, sortField, sortOrder], () => {
    syncQuery()
  }, { deep: true })

  // Available tags from all agents
  const availableTags = computed(() => {
    const agents = agentsRef.value || []
    const tagSet = new Set()
    agents.forEach(a => {
      if (Array.isArray(a.tags)) {
        a.tags.forEach(tag => tagSet.add(tag))
      }
    })
    return Array.from(tagSet).sort()
  })

  // Filtered + sorted agents
  const filteredAgents = computed(() => {
    let result = [...(agentsRef.value || [])]

    // Apply search
    const raw = searchQuery.value.trim()
    if (raw) {
      const idMatch = raw.match(/^#id=(\S+)$/)
      if (idMatch) {
        result = result.filter(a => String(a.id) === idMatch[1])
      } else {
        const q = raw.toLowerCase()
        result = result.filter(a =>
          String(a.name || '').toLowerCase().includes(q) ||
          String(a.agent_url || '').toLowerCase().includes(q) ||
          String(a.last_seen_ip || '').toLowerCase().includes(q) ||
          (a.tags || []).some(tag => String(tag).toLowerCase().includes(q))
        )
      }
    }

    // Apply status filter
    if (statusFilter.value) {
      result = result.filter(a => getAgentStatus(a) === statusFilter.value)
    }

    // Apply mode filter
    if (modeFilter.value) {
      result = result.filter(a => a.mode === modeFilter.value)
    }

    // Apply tag filter
    if (tagFilter.value) {
      result = result.filter(a => (a.tags || []).includes(tagFilter.value))
    }

    // Apply sort
    result.sort((a, b) => {
      let comparison = 0
      switch (sortField.value) {
        case 'name':
          comparison = String(a.name || '').localeCompare(String(b.name || ''))
          break
        case 'http_rules_count':
          comparison = (a.http_rules_count || 0) - (b.http_rules_count || 0)
          break
        case 'l4_rules_count':
          comparison = (a.l4_rules_count || 0) - (b.l4_rules_count || 0)
          break
        case 'last_seen_at':
        default:
          comparison = new Date(a.last_seen_at || 0) - new Date(b.last_seen_at || 0)
          break
      }
      return sortOrder.value === 'asc' ? comparison : -comparison
    })

    return result
  })

  const hasActiveFilters = computed(() =>
    !!statusFilter.value || !!modeFilter.value || !!tagFilter.value || !!searchQuery.value.trim()
  )

  function clearFilters() {
    statusFilter.value = ''
    modeFilter.value = ''
    tagFilter.value = ''
    searchQuery.value = ''
  }

  function toggleSortOrder() {
    sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
  }

  return {
    view,
    statusFilter,
    modeFilter,
    tagFilter,
    sortField,
    sortOrder,
    searchQuery,
    availableTags,
    filteredAgents,
    hasActiveFilters,
    clearFilters,
    toggleSortOrder
  }
}
