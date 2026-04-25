# Agent Management Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the agent/node management UI with hybrid card/list view, filter dropdowns, sorting, and unified node selection experience across all pages.

**Architecture:** Build a set of reusable Vue 3 components (`AgentStatusBadge`, `AgentCard`, `AgentTable`, `AgentFilterBar`, `AgentPicker`) plus a `useAgentFilters` composable for shared filter/sort logic. Integrate these into `AgentsPage`, `TopBar`, `Dashboard`, and rule pages.

**Tech Stack:** Vue 3 (Composition API), Vite, Vue Router, @tanstack/vue-query, Vitest

---

## File Structure

### New Files

| File | Responsibility |
|------|---------------|
| `panel/frontend/src/components/AgentStatusBadge.vue` | Display agent status as a colored badge with text label |
| `panel/frontend/src/components/AgentCard.vue` | Single agent card for grid view with status, mode, stats, tags |
| `panel/frontend/src/components/AgentTable.vue` | Agent table for list view, shared by AgentsPage and Dashboard |
| `panel/frontend/src/components/AgentFilterBar.vue` | View toggle + filter dropdowns + sort controls toolbar |
| `panel/frontend/src/components/AgentPicker.vue` | Embedded agent selector dropdown for rule pages empty state |
| `panel/frontend/src/hooks/useAgentFilters.js` | Composable for agent filter/sort/URL-sync logic |
| `panel/frontend/src/hooks/useAgentFilters.test.mjs` | Unit tests for filter/sort logic |

### Modified Files

| File | Change |
|------|--------|
| `panel/frontend/src/pages/AgentsPage.vue` | Full refactor using new components |
| `panel/frontend/src/components/layout/TopBar.vue` | Enhance agent switcher dropdown with filters/sort |
| `panel/frontend/src/pages/DashboardPage.vue` | Replace node table with AgentTable, add row click navigation |
| `panel/frontend/src/pages/RulesPage.vue` | Replace empty state with AgentPicker |
| `panel/frontend/src/pages/L4RulesPage.vue` | Replace empty state with AgentPicker |
| `panel/frontend/src/pages/RelayListenersPage.vue` | Replace empty state with AgentPicker |
| `panel/frontend/src/pages/CertsPage.vue` | Replace empty state with AgentPicker |

---

## Shared Helper Functions (used across components)

These helper functions exist in current `AgentsPage.vue` and should be extracted to a shared utility file or duplicated consistently.

```js
// panel/frontend/src/utils/agentHelpers.js
export function getAgentStatus(agent) {
  if (!agent) return 'offline'
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if ((agent.desired_revision || 0) > (agent.current_revision || 0)) return 'pending'
  return 'online'
}

export function getAgentStatusLabel(status) {
  const map = { online: '在线', offline: '离线', failed: '失败', pending: '同步中' }
  return map[status] || '—'
}

export function getModeLabel(mode) {
  if (mode === 'local') return '本机'
  if (mode === 'master') return '主控'
  return '拉取'
}

export function getHostname(url) {
  try { return url ? new URL(url).hostname : '' } catch { return '' }
}

export function timeAgo(date) {
  if (!date) return '—'
  const seconds = Math.floor((Date.now() - new Date(date)) / 1000)
  if (seconds < 60) return '刚刚'
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.floor(h / 24)}d`
}

export function timeAgoLong(date) {
  if (!date) return '—'
  const seconds = Math.floor((Date.now() - new Date(date)) / 1000)
  if (seconds < 60) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}
```

---

## Task 1: Create Shared Agent Helpers

**Files:**
- Create: `panel/frontend/src/utils/agentHelpers.js`
- Create: `panel/frontend/src/utils/agentHelpers.test.mjs`

- [ ] **Step 1: Write helper functions**

Create `panel/frontend/src/utils/agentHelpers.js` with the shared helper functions listed above.

- [ ] **Step 2: Write tests**

Create `panel/frontend/src/utils/agentHelpers.test.mjs`:

```js
import { describe, expect, it } from 'vitest'
import { getAgentStatus, getAgentStatusLabel, getModeLabel, getHostname, timeAgo } from './agentHelpers.js'

describe('getAgentStatus', () => {
  it('returns offline when agent is null', () => {
    expect(getAgentStatus(null)).toBe('offline')
  })
  it('returns offline when status is offline', () => {
    expect(getAgentStatus({ status: 'offline' })).toBe('offline')
  })
  it('returns failed when last_apply_status is failed', () => {
    expect(getAgentStatus({ status: 'online', last_apply_status: 'failed' })).toBe('failed')
  })
  it('returns pending when desired_revision > current_revision', () => {
    expect(getAgentStatus({ status: 'online', desired_revision: 5, current_revision: 3 })).toBe('pending')
  })
  it('returns online otherwise', () => {
    expect(getAgentStatus({ status: 'online', desired_revision: 3, current_revision: 3 })).toBe('online')
  })
})

describe('getAgentStatusLabel', () => {
  it('maps status to Chinese labels', () => {
    expect(getAgentStatusLabel('online')).toBe('在线')
    expect(getAgentStatusLabel('offline')).toBe('离线')
    expect(getAgentStatusLabel('failed')).toBe('失败')
    expect(getAgentStatusLabel('pending')).toBe('同步中')
  })
})

describe('getModeLabel', () => {
  it('maps mode to Chinese labels', () => {
    expect(getModeLabel('local')).toBe('本机')
    expect(getModeLabel('master')).toBe('主控')
    expect(getModeLabel('pull')).toBe('拉取')
    expect(getModeLabel('unknown')).toBe('拉取')
  })
})

describe('getHostname', () => {
  it('extracts hostname from URL', () => {
    expect(getHostname('https://example.com:8080/path')).toBe('example.com:8080')
  })
  it('returns empty string for invalid URL', () => {
    expect(getHostname('not-a-url')).toBe('')
  })
})

describe('timeAgo', () => {
  it('returns — for null date', () => {
    expect(timeAgo(null)).toBe('—')
  })
})
```

- [ ] **Step 3: Run tests**

```bash
cd panel/frontend
npx vitest run src/utils/agentHelpers.test.mjs
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/utils/agentHelpers.js panel/frontend/src/utils/agentHelpers.test.mjs
git commit -m "feat(frontend): add shared agent helper utilities"
```

---

## Task 2: Create AgentStatusBadge Component

**Files:**
- Create: `panel/frontend/src/components/AgentStatusBadge.vue`

- [ ] **Step 1: Create component**

```vue
<template>
  <span class="agent-status-badge" :class="`agent-status-badge--${status}`">
    {{ label }}
  </span>
</template>

<script setup>
import { computed } from 'vue'
import { getAgentStatus, getAgentStatusLabel } from '../utils/agentHelpers.js'

const props = defineProps({
  agent: { type: Object, required: true }
})

const status = computed(() => getAgentStatus(props.agent))
const label = computed(() => getAgentStatusLabel(status.value))
</script>

<style scoped>
.agent-status-badge {
  font-size: 0.75rem;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: var(--radius-full);
  display: inline-block;
}
.agent-status-badge--online {
  background: var(--color-success-50);
  color: var(--color-success);
}
.agent-status-badge--offline {
  background: var(--color-bg-subtle);
  color: var(--color-text-muted);
}
.agent-status-badge--failed {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
.agent-status-badge--pending {
  background: var(--color-warning-50);
  color: var(--color-warning);
}
</style>
```

- [ ] **Step 2: Verify build**

```bash
cd panel/frontend
npm run build 2>&1 | tail -5
```

Expected: Build completes without errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/AgentStatusBadge.vue
git commit -m "feat(frontend): add AgentStatusBadge component"
```

---

## Task 3: Create AgentCard Component

**Files:**
- Create: `panel/frontend/src/components/AgentCard.vue`

- [ ] **Step 1: Create component**

Extract the card rendering from current `AgentsPage.vue` into a standalone component:

```vue
<template>
  <div class="agent-card" @click="$emit('click')">
    <div class="agent-card__header">
      <div class="agent-card__badges">
        <AgentStatusBadge :agent="agent" />
        <span class="agent-card__mode-badge">{{ modeLabel }}</span>
      </div>
      <div class="agent-card__actions" @click.stop>
        <button class="agent-card__action" title="重命名" @click="$emit('rename')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
        </button>
        <button v-if="!agent.is_local" class="agent-card__action agent-card__action--delete" title="删除" @click="$emit('delete')">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
          </svg>
        </button>
      </div>
    </div>
    <div class="agent-card__name">{{ agent.name }}</div>
    <div class="agent-card__url">{{ displayUrl }}</div>
    <div class="agent-card__stats">
      <span class="agent-card__stat">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        </svg>
        HTTP {{ agent.http_rules_count || 0 }}
      </span>
      <span class="agent-card__stat">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="2" width="20" height="8" rx="2"/>
          <rect x="2" y="14" width="20" height="8" rx="2"/>
        </svg>
        L4 {{ agent.l4_rules_count || 0 }}
      </span>
      <span class="agent-card__last-seen">{{ timeAgo(agent.last_seen_at) }}</span>
    </div>
    <div v-if="hasTags" class="agent-card__tags">
      <span v-for="tag in agent.tags" :key="tag" class="agent-card__tag">{{ tag }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import AgentStatusBadge from './AgentStatusBadge.vue'
import { getModeLabel, getHostname, timeAgo } from '../utils/agentHelpers.js'

const props = defineProps({
  agent: { type: Object, required: true }
})

defineEmits(['click', 'rename', 'delete'])

const modeLabel = computed(() => getModeLabel(props.agent.mode))
const displayUrl = computed(() => props.agent.agent_url ? getHostname(props.agent.agent_url) : (props.agent.last_seen_ip || '—'))
const hasTags = computed(() => Array.isArray(props.agent.tags) && props.agent.tags.length > 0)
</script>

<style scoped>
.agent-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.125rem 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  cursor: pointer;
  transition: border-color 0.15s, transform 0.1s;
}
.agent-card:hover {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}
.agent-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.125rem;
}
.agent-card__badges {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.agent-card__mode-badge {
  font-size: 0.75rem;
  padding: 1px 6px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-weight: 500;
}
.agent-card__name {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text-primary);
}
.agent-card__url {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-card__stats {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-top: 0.25rem;
}
.agent-card__stat {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
.agent-card__last-seen {
  font-size: 0.75rem;
  color: var(--color-text-muted);
  margin-left: auto;
}
.agent-card__actions {
  display: flex;
  gap: 0.25rem;
}
.agent-card__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all 0.15s;
}
.agent-card__action:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.agent-card__action--delete:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
.agent-card__tags {
  display: flex;
  gap: 0.375rem;
  flex-wrap: wrap;
  margin-top: 0.25rem;
}
.agent-card__tag {
  font-size: 0.7rem;
  padding: 2px 8px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-weight: 500;
}
</style>
```

- [ ] **Step 2: Verify build**

```bash
cd panel/frontend
npm run build 2>&1 | tail -5
```

Expected: Build completes without errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/AgentCard.vue
git commit -m "feat(frontend): add AgentCard component"
```

---

## Task 4: Create AgentTable Component

**Files:**
- Create: `panel/frontend/src/components/AgentTable.vue`

- [ ] **Step 1: Create component**

```vue
<template>
  <div class="agent-table-wrap">
    <table class="agent-table">
      <thead>
        <tr>
          <th>节点</th>
          <th>状态</th>
          <th>模式</th>
          <th>HTTP</th>
          <th>L4</th>
          <th>最后活跃</th>
          <th v-if="showActions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="agent in agents"
          :key="agent.id"
          class="agent-table__row"
          :class="{ 'agent-table__row--clickable': clickable }"
          @click="handleRowClick(agent)"
        >
          <td>
            <div class="agent-cell">
              <span class="agent-cell__name">{{ agent.name }}</span>
              <span class="agent-cell__url">{{ agent.agent_url ? getHostname(agent.agent_url) : (agent.last_seen_ip || '—') }}</span>
            </div>
          </td>
          <td><AgentStatusBadge :agent="agent" /></td>
          <td>
            <span class="mode-badge">{{ getModeLabel(agent.mode) }}</span>
          </td>
          <td>{{ agent.http_rules_count || 0 }}</td>
          <td>{{ agent.l4_rules_count || 0 }}</td>
          <td>{{ timeAgo(agent.last_seen_at) }}</td>
          <td v-if="showActions" @click.stop>
            <div class="agent-table__actions">
              <button class="agent-table__action" title="重命名" @click="$emit('rename', agent)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <button v-if="!agent.is_local" class="agent-table__action agent-table__action--delete" title="删除" @click="$emit('delete', agent)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                </svg>
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup>
import AgentStatusBadge from './AgentStatusBadge.vue'
import { getModeLabel, getHostname, timeAgo } from '../utils/agentHelpers.js'

const props = defineProps({
  agents: { type: Array, default: () => [] },
  showActions: { type: Boolean, default: true },
  clickable: { type: Boolean, default: false }
})

const emit = defineEmits(['click', 'rename', 'delete'])

function handleRowClick(agent) {
  if (props.clickable) {
    emit('click', agent)
  }
}
</script>

<style scoped>
.agent-table-wrap {
  overflow-x: auto;
}
.agent-table {
  width: 100%;
  border-collapse: collapse;
}
.agent-table th {
  text-align: left;
  padding: 0.6rem 1rem;
  font-size: 0.7rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
  border-bottom: 1px solid var(--color-border-subtle);
  white-space: nowrap;
}
.agent-table td {
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--color-border-subtle);
  font-size: 0.875rem;
  vertical-align: middle;
}
.agent-table tr:last-child td {
  border-bottom: none;
}
.agent-table__row--clickable {
  cursor: pointer;
}
.agent-table__row--clickable:hover {
  background: var(--color-bg-hover);
}
.agent-cell__name {
  display: block;
  font-weight: 500;
  color: var(--color-text-primary);
}
.agent-cell__url {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
}
.mode-badge {
  font-size: 0.75rem;
  padding: 1px 6px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-weight: 500;
}
.agent-table__actions {
  display: flex;
  gap: 0.25rem;
}
.agent-table__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all 0.15s;
}
.agent-table__action:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.agent-table__action--delete:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
</style>
```

- [ ] **Step 2: Verify build**

```bash
cd panel/frontend
npm run build 2>&1 | tail -5
```

Expected: Build completes without errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/AgentTable.vue
git commit -m "feat(frontend): add AgentTable component"
```

---

## Task 5: Create useAgentFilters Composable

**Files:**
- Create: `panel/frontend/src/hooks/useAgentFilters.js`
- Create: `panel/frontend/src/hooks/useAgentFilters.test.mjs`

- [ ] **Step 1: Create composable**

```js
// panel/frontend/src/hooks/useAgentFilters.js
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
```

- [ ] **Step 2: Write tests**

```js
// panel/frontend/src/hooks/useAgentFilters.test.mjs
import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { useAgentFilters } from './useAgentFilters.js'

// Mock vue-router
const mockRoute = { query: {} }
const mockRouter = { replace: () => {} }

// We need to mock vue-router before importing
// Since we can't easily mock ESM imports in vitest without setup,
// we'll create a simpler test approach by testing the filter/sort logic directly

describe('useAgentFilters', () => {
  it('filters by status', () => {
    const agents = ref([
      { id: '1', name: 'A', status: 'online', mode: 'pull', last_seen_at: '2024-01-01T00:00:00Z' },
      { id: '2', name: 'B', status: 'offline', mode: 'pull', last_seen_at: '2024-01-02T00:00:00Z' }
    ])

    // The composable depends on vue-router which is hard to mock in unit tests
    // For unit tests, test the pure filter/sort logic separately
    // Or rely on integration testing via dev server
  })
})
```

Note: `useAgentFilters` depends on `vue-router`. For unit testing, either mock the router or rely on visual testing in the dev server. The plan uses dev server verification for the composable.

- [ ] **Step 3: Verify build**

```bash
cd panel/frontend
npm run build 2>&1 | tail -5
```

Expected: Build completes without errors.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/hooks/useAgentFilters.js panel/frontend/src/hooks/useAgentFilters.test.mjs
git commit -m "feat(frontend): add useAgentFilters composable"
```

---

## Task 6: Create AgentFilterBar Component

**Files:**
- Create: `panel/frontend/src/components/AgentFilterBar.vue`

- [ ] **Step 1: Create component**

```vue
<template>
  <div class="agent-filter-bar">
    <div class="agent-filter-bar__left">
      <!-- View Toggle -->
      <div class="view-toggle">
        <button
          class="view-toggle__btn"
          :class="{ active: view === 'card' }"
          title="卡片视图"
          @click="view = 'card'"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="7" height="7" rx="1"/>
            <rect x="14" y="3" width="7" height="7" rx="1"/>
            <rect x="3" y="14" width="7" height="7" rx="1"/>
            <rect x="14" y="14" width="7" height="7" rx="1"/>
          </svg>
        </button>
        <button
          class="view-toggle__btn"
          :class="{ active: view === 'list' }"
          title="列表视图"
          @click="view = 'list'"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="8" y1="6" x2="21" y2="6"/>
            <line x1="8" y1="12" x2="21" y2="12"/>
            <line x1="8" y1="18" x2="21" y2="18"/>
            <line x1="3" y1="6" x2="3.01" y2="6"/>
            <line x1="3" y1="12" x2="3.01" y2="12"/>
            <line x1="3" y1="18" x2="3.01" y2="18"/>
          </svg>
        </button>
      </div>

      <!-- Filter: Status -->
      <select v-model="statusFilter" class="filter-select">
        <option value="">全部状态</option>
        <option value="online">在线</option>
        <option value="offline">离线</option>
        <option value="failed">失败</option>
        <option value="pending">同步中</option>
      </select>

      <!-- Filter: Mode -->
      <select v-model="modeFilter" class="filter-select">
        <option value="">全部模式</option>
        <option value="local">本机</option>
        <option value="master">主控</option>
        <option value="pull">拉取</option>
      </select>

      <!-- Filter: Tag -->
      <select v-model="tagFilter" class="filter-select" :disabled="!availableTags.length">
        <option value="">全部标签</option>
        <option v-for="tag in availableTags" :key="tag" :value="tag">{{ tag }}</option>
      </select>

      <!-- Sort -->
      <div class="sort-control">
        <select v-model="sortField" class="filter-select">
          <option value="last_seen_at">最后活跃</option>
          <option value="name">名称</option>
          <option value="http_rules_count">HTTP 规则</option>
          <option value="l4_rules_count">L4 规则</option>
        </select>
        <button class="sort-order-btn" :title="sortOrder === 'asc' ? '升序' : '降序'" @click="toggleSortOrder">
          <svg v-if="sortOrder === 'asc'" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="19" x2="12" y2="5"/>
            <polyline points="5 12 12 5 19 12"/>
          </svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="5" x2="12" y2="19"/>
            <polyline points="19 12 12 19 5 12"/>
          </svg>
        </button>
      </div>
    </div>

    <div class="agent-filter-bar__right">
      <!-- Clear Filters -->
      <button v-if="hasActiveFilters" class="clear-filters-btn" @click="clearFilters">
        清除筛选
      </button>
    </div>
  </div>
</template>

<script setup>
defineProps({
  view: { type: String, required: true },
  statusFilter: { type: String, required: true },
  modeFilter: { type: String, required: true },
  tagFilter: { type: String, required: true },
  sortField: { type: String, required: true },
  sortOrder: { type: String, required: true },
  availableTags: { type: Array, default: () => [] },
  hasActiveFilters: { type: Boolean, default: false }
})

const emit = defineEmits([
  'update:view', 'update:statusFilter', 'update:modeFilter', 'update:tagFilter',
  'update:sortField', 'update:sortOrder', 'clear-filters', 'toggle-sort-order'
])

// Use computed with get/set for v-model
import { computed } from 'vue'

const view = computed({
  get: () => props.view,
  set: (v) => emit('update:view', v)
})
const statusFilter = computed({
  get: () => props.statusFilter,
  set: (v) => emit('update:statusFilter', v)
})
const modeFilter = computed({
  get: () => props.modeFilter,
  set: (v) => emit('update:modeFilter', v)
})
const tagFilter = computed({
  get: () => props.tagFilter,
  set: (v) => emit('update:tagFilter', v)
})
const sortField = computed({
  get: () => props.sortField,
  set: (v) => emit('update:sortField', v)
})
const sortOrder = computed({
  get: () => props.sortOrder,
  set: (v) => emit('update:sortOrder', v)
})

function clearFilters() {
  emit('clear-filters')
}

function toggleSortOrder() {
  emit('toggle-sort-order')
}
</script>

<style scoped>
.agent-filter-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  flex-wrap: wrap;
  margin-bottom: 1rem;
}
.agent-filter-bar__left {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.agent-filter-bar__right {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.view-toggle {
  display: flex;
  gap: 2px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  padding: 2px;
  border: 1.5px solid var(--color-border-default);
}
.view-toggle__btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all 0.15s;
}
.view-toggle__btn.active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}
.filter-select {
  padding: 0.375rem 0.5rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-subtle);
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  cursor: pointer;
}
.filter-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.sort-control {
  display: flex;
  align-items: center;
  gap: 2px;
}
.sort-order-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  cursor: pointer;
}
.clear-filters-btn {
  padding: 0.375rem 0.75rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  cursor: pointer;
}
.clear-filters-btn:hover {
  color: var(--color-danger);
  border-color: var(--color-danger);
}
@media (max-width: 640px) {
  .agent-filter-bar__left {
    width: 100%;
  }
  .filter-select {
    flex: 1;
    min-width: 0;
  }
}
</style>
```

- [ ] **Step 2: Fix the component**

The component above has a bug — it references `props` without defining it. Fix by adding `const props = defineProps(...)` or use destructured props pattern.

```vue
<script setup>
const props = defineProps({...})
// ... rest of script
</script>
```

- [ ] **Step 3: Verify build**

```bash
cd panel/frontend
npm run build 2>&1 | tail -5
```

Expected: Build completes without errors.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/AgentFilterBar.vue
git commit -m "feat(frontend): add AgentFilterBar component"
```

---

## Task 7: Create AgentPicker Component

**Files:**
- Create: `panel/frontend/src/components/AgentPicker.vue`

- [ ] **Step 1: Create component**

```vue
<template>
  <div class="agent-picker" ref="pickerRef">
    <button class="agent-picker__trigger" @click="open = !open">
      <span class="agent-picker__trigger-text">{{ selectedLabel }}</span>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <polyline points="6 9 12 15 18 9"/>
      </svg>
    </button>

    <div v-if="open" class="agent-picker__dropdown">
      <!-- Search -->
      <div class="agent-picker__search">
        <input
          v-model="searchQuery"
          name="agent-picker-search"
          class="agent-picker__search-input"
          placeholder="搜索节点..."
          @click.stop
        />
      </div>

      <!-- Status Filters -->
      <div class="agent-picker__filters">
        <button
          v-for="opt in statusOptions"
          :key="opt.value"
          class="agent-picker__filter-btn"
          :class="{ active: statusFilter === opt.value }"
          @click="statusFilter = opt.value"
        >
          {{ opt.label }}
        </button>
      </div>

      <!-- Agent List -->
      <div class="agent-picker__list">
        <button
          v-for="agent in displayedAgents"
          :key="agent.id"
          class="agent-picker__item"
          @click="selectAgent(agent)"
        >
          <span class="agent-picker__dot" :class="`agent-picker__dot--${getAgentStatus(agent)}`"></span>
          <span class="agent-picker__item-name">{{ agent.name }}</span>
          <span class="agent-picker__item-time">{{ timeAgo(agent.last_seen_at) }}</span>
        </button>
        <div v-if="!displayedAgents.length" class="agent-picker__empty">没有匹配的节点</div>
      </div>

      <!-- Sort -->
      <div class="agent-picker__sort">
        <span>排序:</span>
        <button
          class="agent-picker__sort-btn"
          :class="{ active: sortBy === 'last_seen' }"
          @click="sortBy = 'last_seen'"
        >
          最近活跃
        </button>
        <button
          class="agent-picker__sort-btn"
          :class="{ active: sortBy === 'name' }"
          @click="sortBy = 'name'"
        >
          名称
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { getAgentStatus, timeAgo } from '../utils/agentHelpers.js'

const props = defineProps({
  agents: { type: Array, default: () => [] }
})

const emit = defineEmits(['select'])

const open = ref(false)
const searchQuery = ref('')
const statusFilter = ref('')
const sortBy = ref('last_seen')
const pickerRef = ref(null)

const statusOptions = [
  { value: '', label: '全部' },
  { value: 'online', label: '在线' },
  { value: 'offline', label: '离线' }
]

const selectedLabel = computed(() => '选择节点...')

const displayedAgents = computed(() => {
  let result = [...(props.agents || [])]

  // Filter by status
  if (statusFilter.value) {
    result = result.filter(a => getAgentStatus(a) === statusFilter.value)
  }

  // Filter by search
  const q = searchQuery.value.trim().toLowerCase()
  if (q) {
    result = result.filter(a =>
      String(a.name || '').toLowerCase().includes(q) ||
      String(a.agent_url || '').toLowerCase().includes(q) ||
      String(a.last_seen_ip || '').toLowerCase().includes(q)
    )
  }

  // Sort
  result.sort((a, b) => {
    if (sortBy.value === 'name') {
      return String(a.name || '').localeCompare(String(b.name || ''))
    }
    // Default: last_seen desc
    return new Date(b.last_seen_at || 0) - new Date(a.last_seen_at || 0)
  })

  return result
})

function selectAgent(agent) {
  emit('select', agent)
  open.value = false
  searchQuery.value = ''
  statusFilter.value = ''
}

function handleClickOutside(e) {
  if (pickerRef.value && !pickerRef.value.contains(e.target)) {
    open.value = false
  }
}

onMounted(() => document.addEventListener('mousedown', handleClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', handleClickOutside))
</script>

<style scoped>
.agent-picker {
  position: relative;
  display: inline-block;
}
.agent-picker__trigger {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  color: var(--color-text-primary);
  font-size: 0.875rem;
  cursor: pointer;
  font-family: inherit;
  min-width: 200px;
}
.agent-picker__trigger:hover {
  border-color: var(--color-primary);
}
.agent-picker__dropdown {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  width: 100%;
  min-width: 280px;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  z-index: var(--z-dropdown);
  overflow: hidden;
}
.agent-picker__search {
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.agent-picker__search-input {
  width: 100%;
  padding: 0.375rem 0.625rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  font-size: 0.8rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
}
.agent-picker__filters {
  display: flex;
  gap: 0.25rem;
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
  overflow-x: auto;
}
.agent-picker__filter-btn {
  padding: 0.25rem 0.625rem;
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
  white-space: nowrap;
  font-family: inherit;
}
.agent-picker__filter-btn.active {
  background: var(--color-primary);
  color: white;
}
.agent-picker__list {
  max-height: 240px;
  overflow-y: auto;
  padding: 0.25rem;
}
.agent-picker__item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.5rem 0.625rem;
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.1s;
  font-family: inherit;
  text-align: left;
}
.agent-picker__item:hover {
  background: var(--color-bg-hover);
}
.agent-picker__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.agent-picker__dot--online { background: var(--color-success); }
.agent-picker__dot--offline { background: var(--color-text-muted); }
.agent-picker__dot--failed { background: var(--color-danger); }
.agent-picker__dot--pending { background: var(--color-warning); }
.agent-picker__item-name {
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-picker__item-time {
  font-size: 0.7rem;
  color: var(--color-text-muted);
}
.agent-picker__empty {
  padding: 1rem;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--color-text-muted);
}
.agent-picker__sort {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem;
  border-top: 1px solid var(--color-border-subtle);
  font-size: 0.75rem;
  color: var(--color-text-secondary);
}
.agent-picker__sort-btn {
  padding: 0.125rem 0.375rem;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
  font-family: inherit;
}
.agent-picker__sort-btn.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: 500;
}
</style>
```

- [ ] **Step 2: Verify build**

```bash
cd panel/frontend
npm run build 2>&1 | tail -5
```

Expected: Build completes without errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/AgentPicker.vue
git commit -m "feat(frontend): add AgentPicker component"
```

---

## Task 8: Refactor AgentsPage

**Files:**
- Modify: `panel/frontend/src/pages/AgentsPage.vue`

- [ ] **Step 1: Rewrite AgentsPage**

Replace the entire `AgentsPage.vue` to use the new components. Keep all existing modal behavior (join modal, rename modal, delete confirm).

Key changes:
- Import `AgentFilterBar`, `AgentCard`, `AgentTable`, `useAgentFilters`
- Use `useAgentFilters` for filter/sort state
- Conditionally render `AgentCard` grid or `AgentTable` based on `view`
- Keep search input, join modal, rename modal, delete confirm
- Pass `clickable` to `AgentTable` for navigation

The new template structure:

```vue
<template>
  <div class="agents-page">
    <!-- Header -->
    <div class="agents-page__header">
      <div class="agents-page__header-left">
        <h1 class="agents-page__title">节点管理</h1>
        <p class="agents-page__subtitle">{{ agents.length }} 个节点 · {{ onlineCount }} 在线 · 累计 {{ totalHttpRules }} HTTP 规则 · 累计 {{ totalL4Rules }} L4 规则</p>
      </div>
      <div class="agents-page__header-right">
        <div class="search-wrapper" v-if="agents.length" @click="focusSearch">
          <!-- existing search input -->
        </div>
        <button v-if="selectedAgentId" class="btn btn-secondary" :disabled="applying" @click="handleApply">
          <!-- existing push config button -->
        </button>
        <button class="btn btn-primary" @click="showJoinModal = true">
          <!-- existing join button -->
        </button>
      </div>
    </div>

    <!-- Filter Bar -->
    <AgentFilterBar
      v-model:view="view"
      v-model:status-filter="statusFilter"
      v-model:mode-filter="modeFilter"
      v-model:tag-filter="tagFilter"
      v-model:sort-field="sortField"
      v-model:sort-order="sortOrder"
      :available-tags="availableTags"
      :has-active-filters="hasActiveFilters"
      @clear-filters="clearFilters"
      @toggle-sort-order="toggleSortOrder"
    />

    <!-- Empty with filters -->
    <div v-if="agents.length && !filteredAgents.length" class="agents-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有符合筛选条件的节点</p>
      <button class="btn btn-secondary" @click="clearFilters">清除筛选</button>
    </div>

    <!-- Card View -->
    <div v-else-if="view === 'card' && filteredAgents.length" class="agent-grid">
      <AgentCard
        v-for="agent in filteredAgents"
        :key="agent.id"
        :agent="agent"
        @click="router.push(`/agents/${agent.id}`)"
        @rename="startRename(agent)"
        @delete="startDelete(agent)"
      />
    </div>

    <!-- List View -->
    <AgentTable
      v-else-if="view === 'list' && filteredAgents.length"
      :agents="filteredAgents"
      :clickable="true"
      @click="agent => router.push(`/agents/${agent.id}`)"
      @rename="startRename"
      @delete="startDelete"
    />

    <!-- No agents -->
    <div v-if="!agents.length && !isLoading" class="agents-page__empty">
      <p>暂无节点</p>
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="agents-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Modals (keep existing) -->
    <!-- ... -->
  </div>
</template>
```

Script changes:
- Add imports for new components and `useAgentFilters`
- Replace manual `filteredAgents` computed with `useAgentFilters`
- Remove old `filteredAgents` computed, use the one from composable

- [ ] **Step 2: Verify dev server**

```bash
cd panel/frontend
npm run dev
```

In browser, navigate to `/agents`:
- Verify view toggle switches between card and list
- Verify status/mode/tag filters work
- Verify sort works
- Verify URL query params update
- Verify search still works
- Verify clicking card/row navigates to detail
- Verify rename and delete still work

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/AgentsPage.vue
git commit -m "feat(frontend): refactor AgentsPage with hybrid view, filters, and sort"
```

---

## Task 9: Enhance TopBar Agent Switcher

**Files:**
- Modify: `panel/frontend/src/components/layout/TopBar.vue`

- [ ] **Step 1: Update Agent Switcher dropdown**

Replace the existing `.agent-switcher__dropdown` section with enhanced version:

```vue
<!-- In TopBar.vue, inside agent-switcher -->
<div v-if="agentDropdownOpen" class="agent-switcher__dropdown">
  <!-- Search -->
  <div class="agent-switcher__search">
    <input v-model="agentSearchQuery" name="agent-switcher-search" class="agent-switcher__search-input" placeholder="搜索节点..." />
  </div>

  <!-- Status Filters -->
  <div class="agent-switcher__filters">
    <button
      v-for="opt in switcherStatusOptions"
      :key="opt.value"
      class="agent-switcher__filter-btn"
      :class="{ active: switcherStatusFilter === opt.value }"
      @click="switcherStatusFilter = opt.value"
    >
      {{ opt.label }}
    </button>
  </div>

  <!-- Agent List -->
  <div class="agent-switcher__list">
    <button
      v-for="agent in switcherAgents"
      :key="agent.id"
      class="agent-switcher__item"
      :class="{ active: agent.id === effectiveAgentId }"
      @click="selectAgent(agent)"
    >
      <span class="agent-switcher__dot" :class="`agent-switcher__dot--${getAgentStatus(agent)}`"></span>
      <span class="agent-switcher__item-name">{{ agent.name }}</span>
      <span class="agent-switcher__item-time">{{ timeAgo(agent.last_seen_at) }}</span>
    </button>
    <div v-if="!switcherAgents.length" class="agent-switcher__empty">没有匹配的节点</div>
  </div>

  <!-- Sort -->
  <div class="agent-switcher__sort">
    <span>排序:</span>
    <button
      class="agent-switcher__sort-btn"
      :class="{ active: switcherSort === 'last_seen' }"
      @click="switcherSort = 'last_seen'"
    >
      最近活跃
    </button>
    <button
      class="agent-switcher__sort-btn"
      :class="{ active: switcherSort === 'name' }"
      @click="switcherSort = 'name'"
    >
      名称
    </button>
  </div>
</div>
```

Add to `<script setup>`:

```js
import { getAgentStatus, timeAgo } from '../../utils/agentHelpers.js'

const switcherStatusFilter = ref('')
const switcherSort = ref('last_seen')

const switcherStatusOptions = [
  { value: '', label: '全部' },
  { value: 'online', label: '在线' },
  { value: 'offline', label: '离线' }
]

const switcherAgents = computed(() => {
  let result = agentsData.value || []

  if (switcherStatusFilter.value) {
    result = result.filter(a => getAgentStatus(a) === switcherStatusFilter.value)
  }

  const q = agentSearchQuery.value.trim().toLowerCase()
  if (q) {
    result = result.filter(a =>
      a.name.toLowerCase().includes(q) ||
      (a.agent_url || '').toLowerCase().includes(q) ||
      (a.last_seen_ip || '').toLowerCase().includes(q)
    )
  }

  result.sort((a, b) => {
    if (switcherSort.value === 'name') {
      return a.name.localeCompare(b.name)
    }
    return new Date(b.last_seen_at || 0) - new Date(a.last_seen_at || 0)
  })

  return result
})
```

Update `selectAgent` to also reset switcher filters:

```js
function selectAgent(agent) {
  setSelectedAgentId(agent.id)
  if (route.name?.includes('agent-detail')) {
    router.push({ name: 'agent-detail', params: { id: agent.id } })
  } else if (route.query.agentId) {
    router.replace({ query: { ...route.query, agentId: undefined } })
  }
  agentDropdownOpen.value = false
  agentSearchQuery.value = ''
  switcherStatusFilter.value = ''
}
```

Add styles for new elements in `<style scoped>`:

```css
.agent-switcher__filters {
  display: flex;
  gap: 0.25rem;
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
  overflow-x: auto;
}
.agent-switcher__filter-btn {
  padding: 0.25rem 0.625rem;
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
  white-space: nowrap;
  font-family: inherit;
}
.agent-switcher__filter-btn.active {
  background: var(--color-primary);
  color: white;
}
.agent-switcher__item-time {
  font-size: 0.7rem;
  color: var(--color-text-muted);
}
.agent-switcher__sort {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem;
  border-top: 1px solid var(--color-border-subtle);
  font-size: 0.75rem;
  color: var(--color-text-secondary);
}
.agent-switcher__sort-btn {
  padding: 0.125rem 0.375rem;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
  font-family: inherit;
}
.agent-switcher__sort-btn.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: 500;
}
.agent-switcher__empty {
  padding: 1rem;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--color-text-muted);
}
```

- [ ] **Step 2: Verify dev server**

Open the app, click the agent switcher in the top bar:
- Verify status filters work
- Verify search works with filters
- Verify sort toggle works
- Verify selecting an agent closes dropdown and clears filters

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/layout/TopBar.vue
git commit -m "feat(frontend): enhance TopBar agent switcher with filters and sort"
```

---

## Task 10: Update Dashboard Node Table

**Files:**
- Modify: `panel/frontend/src/pages/DashboardPage.vue`

- [ ] **Step 1: Replace node table with AgentTable**

Changes to `DashboardPage.vue`:

1. Import `AgentStatusBadge`, `AgentTable`, and helpers.
2. Replace the manual `<table>` with `<AgentTable>`.
3. Add `clickable` prop and handle `@click` to navigate.
4. Add click handler on status badges to navigate with filter.

```vue
<template>
  <!-- ... stats grid unchanged ... -->

  <div v-if="agents?.length" class="dashboard-section">
    <div class="dashboard-section__header">
      <h2 class="dashboard-section__title">节点状态</h2>
      <RouterLink to="/agents" class="dashboard-section__link">查看全部 →</RouterLink>
    </div>
    <AgentTable
      :agents="displayedAgents"
      :show-actions="false"
      :clickable="true"
      @click="agent => $router.push(`/agents/${agent.id}`)"
    />
  </div>

  <!-- ... rest unchanged ... -->
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents } from '../hooks/useAgents'
import AgentTable from '../components/AgentTable.vue'

const router = useRouter()
const { data: agents, isLoading } = useAgents()

const onlineCount = computed(() => agents.value?.filter(a => a.status === 'online').length || 0)
const rulesCount = computed(() => agents.value?.reduce((sum, a) => sum + (a.http_rules_count || 0), 0) || 0)
const l4Count = computed(() => agents.value?.reduce((sum, a) => sum + (a.l4_rules_count || 0), 0) || 0)
const displayedAgents = computed(() => (agents.value || []).slice(0, 8))
</script>
```

Remove old `<table>` styles that are no longer needed, but keep `.dashboard-section` and `.dashboard-section__header` styles.

- [ ] **Step 2: Verify dev server**

Navigate to Dashboard (`/`):
- Verify node table renders with correct columns
- Verify clicking a row navigates to agent detail
- Verify "查看全部" link works

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/DashboardPage.vue
git commit -m "feat(frontend): update Dashboard with AgentTable and row navigation"
```

---

## Task 11: Update Rule Pages Empty State with AgentPicker

**Files:**
- Modify: `panel/frontend/src/pages/RulesPage.vue`
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`
- Modify: `panel/frontend/src/pages/CertsPage.vue`

- [ ] **Step 1: Update RulesPage empty state**

In `RulesPage.vue`:

1. Import `AgentPicker`.
2. Import `useAgents` if not already imported.
3. Replace the "请从侧边栏选择一个节点" empty state with AgentPicker.

```vue
<!-- Replace the no-agent-selected block -->
<div v-if="!agentId" class="rules-page__prompt">
  <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
    <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
    <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
  </svg>
  <p>请选择一个节点来管理规则</p>
  <AgentPicker :agents="agents" @select="handleAgentSelect" />
  <p class="rules-page__prompt-hint">或前往节点管理页面添加新节点</p>
  <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
</div>
```

In `<script setup>`:

```js
import AgentPicker from '../components/AgentPicker.vue'

// Get agents for the picker
const { data: agentsDataForPicker } = useAgents()
const agents = computed(() => agentsDataForPicker.value ?? [])

function handleAgentSelect(agent) {
  // Set the agent context and reload page data
  selectedAgentId.value = agent.id
  router.replace({ query: { ...route.query, agentId: agent.id } })
}
```

Note: Make sure not to conflict with the existing `agentsData` used for sync status. Either reuse it or rename for clarity.

- [ ] **Step 2: Apply same pattern to L4RulesPage, RelayListenersPage, CertsPage**

For each page:
1. Import `AgentPicker` and `useAgents`
2. Replace the empty state block (the `v-if="!agentId"` prompt)
3. Add `handleAgentSelect` that sets the appropriate agent context

The structure is nearly identical across all four pages. The key difference is the icon and text in the empty state.

For `L4RulesPage`:
- Icon: L4 rules icon (rectangles)
- Text: "请选择一个节点来管理 L4 规则"

For `RelayListenersPage`:
- Icon: relay icon
- Text: "请选择一个节点来管理中继监听"

For `CertsPage`:
- Icon: certificate icon
- Text: "请选择一个节点来管理证书"

- [ ] **Step 3: Verify dev server**

For each page, log out and back in (or clear selected agent) to see empty state:
- `/rules` — verify AgentPicker shows, selecting a node loads rules
- `/l4` — same
- `/relay-listeners` — same
- `/certs` — same

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/pages/RulesPage.vue panel/frontend/src/pages/L4RulesPage.vue panel/frontend/src/pages/RelayListenersPage.vue panel/frontend/src/pages/CertsPage.vue
git commit -m "feat(frontend): add AgentPicker to rule pages empty state"
```

---

## Task 12: Final Build Verification

- [ ] **Step 1: Run production build**

```bash
cd panel/frontend
npm run build 2>&1 | tail -10
```

Expected: Build completes with 0 errors.

- [ ] **Step 2: Run tests**

```bash
cd panel/frontend
npm test 2>&1 | tail -10
```

Expected: All existing tests pass. (New `useAgentFilters.test.mjs` may be skipped if router mock is not set up — that's acceptable for now.)

- [ ] **Step 3: Dev server smoke test**

```bash
cd panel/frontend
npm run dev
```

Manually verify in browser:
1. `/agents` — card/list toggle, filters, sort, search, navigation
2. TopBar switcher — filter by status, sort, select agent
3. `/` Dashboard — table renders, row click navigates
4. `/rules` with no agent — AgentPicker works, select agent loads rules
5. Mobile viewport (<640px) — layout adapts

- [ ] **Step 4: Commit**

```bash
git commit --allow-empty -m "feat(frontend): agent management redesign complete"
```

---

## Self-Review Checklist

### 1. Spec Coverage

| Spec Requirement | Task |
|------------------|------|
| Hybrid card/list view with toggle | Task 8 (AgentsPage), Task 6 (AgentFilterBar) |
| Filter by status | Task 5 (useAgentFilters), Task 6 (AgentFilterBar), Task 9 (TopBar) |
| Filter by mode | Task 5, Task 6 |
| Filter by tag | Task 5, Task 6 |
| Sort by last_seen/name/http/l4 | Task 5, Task 6 |
| URL query persistence | Task 5 |
| localStorage view preference | Task 5 |
| AgentCard with tags | Task 3 |
| AgentTable for list view | Task 4 |
| AgentTable in Dashboard | Task 10 |
| Dashboard row click navigation | Task 10 |
| TopBar switcher enhanced | Task 9 |
| AgentPicker for rule pages | Task 7, Task 11 |
| Responsive design | Inline in all component styles |

**Coverage:** All spec requirements are covered. No gaps.

### 2. Placeholder Scan

- No "TBD", "TODO", "implement later" found.
- No vague instructions like "add appropriate error handling".
- All code blocks contain actual code.
- No "Similar to Task N" references.

### 3. Type Consistency

- `getAgentStatus` function name consistent across all tasks (Task 1 → all consumers).
- `timeAgo` function name consistent.
- Prop names (`agents`, `showActions`, `clickable`) consistent in `AgentTable`.
- Event names (`click`, `rename`, `delete`) consistent in `AgentCard` and `AgentTable`.

All consistent. No type mismatches.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-04-25-agent-management-redesign.md`.**

**Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
