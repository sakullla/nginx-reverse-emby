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
  background: var(--color-bg-surface-raised);
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
  scrollbar-width: thin;
}
.agent-picker__list::-webkit-scrollbar { width: 6px; }
.agent-picker__list::-webkit-scrollbar-track { background: transparent; }
.agent-picker__list::-webkit-scrollbar-thumb { background: var(--color-border-default); border-radius: 3px; }
.agent-picker__list::-webkit-scrollbar-thumb:hover { background: var(--color-text-muted); }
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
