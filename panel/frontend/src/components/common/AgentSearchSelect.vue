<template>
  <div class="agent-search-select" ref="rootRef">
    <button
      type="button"
      class="agent-search-select__trigger"
      :aria-expanded="open ? 'true' : 'false'"
      aria-haspopup="listbox"
      @click="toggleOpen"
    >
      <span
        v-if="selectedAgent"
        class="agent-search-select__status-dot"
        :class="`agent-search-select__status-dot--${getAgentStatus(selectedAgent)}`"
      />
      <span class="agent-search-select__label">{{ selectedLabel }}</span>
      <svg class="agent-search-select__chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <polyline points="6 9 12 15 18 9" />
      </svg>
    </button>

    <div v-if="open" class="agent-search-select__dropdown" role="listbox">
      <div class="agent-search-select__search">
        <input
          ref="searchInputRef"
          v-model="searchQuery"
          type="search"
          class="agent-search-select__search-input"
          placeholder="搜索节点..."
          aria-label="搜索节点"
          @keydown.esc.prevent="close"
        />
      </div>

      <div class="agent-search-select__filters">
        <button
          v-for="opt in statusOptions"
          :key="opt.value"
          type="button"
          class="agent-search-select__filter-btn"
          :class="`agent-search-select__filter-btn--${opt.value || 'all'} ${statusFilter === opt.value ? 'agent-search-select__filter-btn--active' : ''}`"
          @click="statusFilter = opt.value"
        >
          {{ opt.label }}
        </button>
      </div>

      <div class="agent-search-select__list">
        <button
          type="button"
          class="agent-search-select__option"
          :class="{ 'agent-search-select__option--active': isAllSelected }"
          role="option"
          :aria-selected="isAllSelected ? 'true' : 'false'"
          @click="selectAll"
        >
          <span class="agent-search-select__option-name">全部节点</span>
        </button>

        <button
          v-for="agent in displayedAgents"
          :key="agent.id"
          type="button"
          class="agent-search-select__option"
          :class="{ 'agent-search-select__option--active': !isAllSelected && agent.id === modelValue }"
          role="option"
          :aria-selected="(!isAllSelected && agent.id === modelValue) ? 'true' : 'false'"
          @click="selectAgent(agent)"
        >
          <span
            class="agent-search-select__status-dot"
            :class="`agent-search-select__status-dot--${getAgentStatus(agent)}`"
          />
          <span class="agent-search-select__option-name">{{ agent.name || agent.id }}</span>
          <span class="agent-search-select__option-time">{{ timeAgo(agent.last_seen_at) }}</span>
        </button>

        <div v-if="!displayedAgents.length" class="agent-search-select__empty">
          没有匹配的节点
        </div>
      </div>

      <div class="agent-search-select__sort">
        <span>排序:</span>
        <button
          type="button"
          class="agent-search-select__sort-btn"
          :class="{ 'agent-search-select__sort-btn--active': sortBy === 'last_seen', 'agent-search-select__sort-btn--last_seen': true }"
          @click="sortBy = 'last_seen'"
        >
          最近活跃
        </button>
        <button
          type="button"
          class="agent-search-select__sort-btn"
          :class="{ 'agent-search-select__sort-btn--active': sortBy === 'name', 'agent-search-select__sort-btn--name': true }"
          @click="sortBy = 'name'"
        >
          名称
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { getAgentStatus, timeAgo } from '../../utils/agentHelpers.js'
import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from '../../utils/agentFilter.js'

const props = defineProps({
  modelValue: { type: String, default: ALL_AGENTS_FILTER },
  agents: { type: Array, default: () => [] }
})

const emit = defineEmits(['update:modelValue'])

const open = ref(false)
const searchQuery = ref('')
const statusFilter = ref('')
const sortBy = ref('last_seen')
const rootRef = ref(null)
const searchInputRef = ref(null)

const statusOptions = [
  { value: '', label: '全部' },
  { value: 'online', label: '在线' },
  { value: 'offline', label: '离线' }
]

const isAllSelected = computed(() => {
  const normalized = normalizeAgentFilter(props.modelValue)
  return !normalized || isAllAgentsFilter(normalized)
})

const selectedAgent = computed(() => {
  if (isAllSelected.value) return null
  const id = normalizeAgentFilter(props.modelValue)
  return (props.agents || []).find((agent) => agent?.id === id) || null
})

const selectedLabel = computed(() => {
  if (isAllSelected.value) return '全部节点'
  if (selectedAgent.value) return selectedAgent.value.name || selectedAgent.value.id
  const id = normalizeAgentFilter(props.modelValue)
  return id || '全部节点'
})

const displayedAgents = computed(() => {
  let result = [...(props.agents || [])]

  if (statusFilter.value) {
    result = result.filter((agent) => getAgentStatus(agent) === statusFilter.value)
  }

  const q = searchQuery.value.trim().toLowerCase()
  if (q) {
    result = result.filter((agent) =>
      String(agent.name || '').toLowerCase().includes(q)
    )
  }

  result.sort((a, b) => {
    if (sortBy.value === 'name') {
      return String(a.name || '').localeCompare(String(b.name || ''))
    }
    return new Date(b.last_seen_at || 0) - new Date(a.last_seen_at || 0)
  })

  return result
})

function selectAll() {
  emit('update:modelValue', ALL_AGENTS_FILTER)
  close()
}

function selectAgent(agent) {
  if (!agent?.id) return
  emit('update:modelValue', String(agent.id))
  close()
}

function close() {
  open.value = false
  searchQuery.value = ''
  statusFilter.value = ''
  sortBy.value = 'last_seen'
}

function toggleOpen() {
  open.value = !open.value
  if (!open.value) close()
}

function handleClickOutside(event) {
  if (rootRef.value && !rootRef.value.contains(event.target)) {
    close()
  }
}

watch(open, async (value) => {
  if (!value) return
  await nextTick()
  searchInputRef.value?.focus?.()
})

onMounted(() => document.addEventListener('mousedown', handleClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', handleClickOutside))
</script>

<style scoped>
.agent-search-select {
  position: relative;
  min-width: 180px;
}

.agent-search-select__trigger {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  min-height: 34px;
  padding: 0.35rem 0.65rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.875rem;
  font-family: inherit;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.agent-search-select__trigger:hover {
  border-color: var(--color-primary);
  background: var(--color-bg-hover);
}

.agent-search-select__trigger:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.agent-search-select__label {
  flex: 1;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-search-select__chevron {
  flex-shrink: 0;
  color: var(--color-text-muted);
}

.agent-search-select__dropdown {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: var(--z-dropdown);
  width: min(320px, 80vw);
  background: var(--color-bg-surface-raised);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  overflow: hidden;
}

.agent-search-select__search {
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.agent-search-select__search-input {
  width: 100%;
  padding: 0.35rem 0.55rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
}

.agent-search-select__search-input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.agent-search-select__filters {
  display: flex;
  gap: 0.25rem;
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
  overflow-x: auto;
}

.agent-search-select__filter-btn {
  padding: 0.25rem 0.625rem;
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
  white-space: nowrap;
  font-family: inherit;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.agent-search-select__filter-btn:hover {
  background: var(--color-bg-hover);
}

.agent-search-select__filter-btn--active {
  background: var(--color-primary);
  color: white;
}

.agent-search-select__list {
  max-height: 240px;
  overflow-y: auto;
  padding: 0.25rem;
  scrollbar-width: thin;
}

.agent-search-select__option {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.5rem 0.65rem;
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  font-family: inherit;
  text-align: left;
  transition: background var(--duration-fast) var(--ease-default);
}

.agent-search-select__option:hover {
  background: var(--color-bg-hover);
}

.agent-search-select__option--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.agent-search-select__option-name {
  flex: 1;
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-search-select__option-time {
  font-size: 0.7rem;
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.agent-search-select__empty {
  padding: 1rem;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--color-text-muted);
}

.agent-search-select__sort {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem;
  border-top: 1px solid var(--color-border-subtle);
  font-size: 0.75rem;
  color: var(--color-text-secondary);
}

.agent-search-select__sort-btn {
  padding: 0.125rem 0.375rem;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
  font-family: inherit;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.agent-search-select__sort-btn:hover {
  background: var(--color-bg-hover);
}

.agent-search-select__sort-btn--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: 500;
}

.agent-search-select__status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.agent-search-select__status-dot--online {
  background: var(--color-success);
}

.agent-search-select__status-dot--offline {
  background: var(--color-text-muted);
}

.agent-search-select__status-dot--failed {
  background: var(--color-danger);
}

.agent-search-select__status-dot--pending {
  background: var(--color-warning);
}
</style>
