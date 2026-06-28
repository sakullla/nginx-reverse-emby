import { computed, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getAgentStatus } from '../utils/agentHelpers.js'

const STORAGE_KEY = 'agent-list-view'

function normalizeAgentView(value) {
  const raw = Array.isArray(value) ? value[0] : value
  const normalized = String(raw || '').trim().toLowerCase()
  return normalized === 'list' ? 'list' : 'monitor'
}

function lastSeenAtRecencyRank(agent, nowMs) {
  const time = new Date(agent.last_seen_at || 0).getTime()
  if (Number.isNaN(time)) return 0
  const minutesAgo = Math.floor((nowMs - time) / 60000)
  if (minutesAgo < 1) return 4
  if (minutesAgo < 5) return 3
  if (minutesAgo < 15) return 2
  if (minutesAgo < 60) return 1
  return 0
}

function totalRulesCount(agent) {
  return (agent.http_rules_count || 0) + (agent.l4_rules_count || 0)
}

function arraysShallowEqual(a, b) {
  if (a === b) return true
  if (!a || !b || a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false
  }
  return true
}

export function useAgentFilters(agentsRef) {
  const route = useRoute()
  const router = useRouter()

  // View preference (monitor/list) with legacy card fallback.
  const view = ref(normalizeAgentView(route.query.view || localStorage.getItem(STORAGE_KEY)))
  watch(view, (v) => {
    const normalized = normalizeAgentView(v)
    if (normalized !== v) {
      view.value = normalized
      return
    }
    localStorage.setItem(STORAGE_KEY, normalized)
    syncQuery({ view: normalized })
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

  // Reactive wall-clock for time-dependent recency buckets.
  // Updated every minute so last_seen_at ordering stays fresh while the page is open.
  const now = ref(Date.now())
  const nowInterval = setInterval(() => {
    now.value = Date.now()
  }, 60000)
  onScopeDispose(() => clearInterval(nowInterval))

  // Sync filters/sort to URL query
  function syncQuery(overrides = {}) {
    const query = { ...route.query }

    // Remove cleared filter keys so stale values don't persist
    if (!statusFilter.value) delete query.status
    else query.status = statusFilter.value

    if (!modeFilter.value) delete query.mode
    else query.mode = modeFilter.value

    if (!tagFilter.value) delete query.tag
    else query.tag = tagFilter.value

    query.view = view.value
    query.sort = sortField.value
    query.order = sortOrder.value

    Object.assign(query, overrides)

    // Remove empty values
    Object.keys(query).forEach(key => {
      if (!query[key] && key !== 'sort' && key !== 'order' && key !== 'view') {
        delete query[key]
      }
    })
    router.replace({ query })
  }

  watch([statusFilter, modeFilter, tagFilter, sortField, sortOrder, searchQuery], () => {
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
  let previousFilteredAgents = []
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
    const direction = sortOrder.value === 'asc' ? 1 : -1
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
          comparison = lastSeenAtRecencyRank(a, now.value) - lastSeenAtRecencyRank(b, now.value)
          if (comparison === 0) {
            comparison = totalRulesCount(a) - totalRulesCount(b)
          }
          break
      }
      if (comparison !== 0) return comparison * direction
      return String(a.id || '').localeCompare(String(b.id || ''))
    })

    if (arraysShallowEqual(previousFilteredAgents, result)) {
      return previousFilteredAgents
    }
    previousFilteredAgents = result
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
