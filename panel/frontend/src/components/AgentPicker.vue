<template>
  <div class="agent-picker" ref="pickerRef">
    <button ref="triggerRef" class="agent-picker__trigger" @click="open = !open">
      <span class="agent-picker__trigger-text">{{ selectedLabel }}</span>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <polyline points="6 9 12 15 18 9"/>
      </svg>
    </button>

    <Teleport to="body">
      <div
        v-if="open"
        ref="dropdownRef"
        class="agent-picker__dropdown"
        :style="dropdownStyle"
      >
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
            v-if="showAllOption"
            class="agent-picker__item agent-picker__item--all"
            @click="selectAll()"
          >
            <span class="agent-picker__item-name">{{ allLabel }}</span>
          </button>
          <button
            v-for="agent in displayedAgents"
            :key="agent.id || agent.agent_id"
            class="agent-picker__item"
            @click="selectAgent(agent)"
          >
            <span v-if="agent.status != null || agent.desired_revision != null" class="agent-picker__dot" :class="`agent-picker__dot--${getAgentStatus(agent)}`"></span>
            <span class="agent-picker__item-name">{{ agent.name }}</span>
            <span v-if="agent.last_seen_at" class="agent-picker__item-time">{{ timeAgo(agent.last_seen_at) }}</span>
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
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { getAgentStatus, timeAgo } from '../utils/agentHelpers.js'

const props = defineProps({
  agents: { type: Array, default: () => [] },
  modelValue: { type: Object, default: null },
  modelId: { type: String, default: null },
  showAllOption: { type: Boolean, default: false },
  allLabel: { type: String, default: '全部节点' },
  allValue: { type: String, default: '' }
})

const emit = defineEmits(['select', 'update:modelValue', 'update:modelId'])

const open = ref(false)
const searchQuery = ref('')
const statusFilter = ref('')
const sortBy = ref('last_seen')
const pickerRef = ref(null)
const triggerRef = ref(null)
const dropdownRef = ref(null)
const dropdownStyle = ref({})

const statusOptions = [
  { value: '', label: '全部' },
  { value: 'online', label: '在线' },
  { value: 'offline', label: '离线' }
]

const selectedLabel = computed(() => {
  if (props.showAllOption && (props.modelId === props.allValue || props.modelId == null)) {
    return props.allLabel
  }
  if (props.modelId != null) {
    const found = props.agents.find(a => a.agent_id === props.modelId || a.id === props.modelId)
    if (found) return found.name
    return props.modelId
  }
  return props.modelValue ? props.modelValue.name : '选择节点...'
})

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
      String(a.ddns_domain || '').toLowerCase().includes(q) ||
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

function updateDropdownPosition() {
  if (!open.value || !triggerRef.value) return
  const rect = triggerRef.value.getBoundingClientRect()
  const viewportWidth = window.innerWidth
  const viewportHeight = window.innerHeight

  if (viewportWidth <= 640) {
    const margin = 12
    const bottomNavHeight = 64
    const safeBottom = viewportHeight - bottomNavHeight - 8
    const estimatedHeight = Math.min(viewportHeight * 0.6, 360)

    let top = rect.bottom + 6
    if (top + estimatedHeight > safeBottom) {
      const above = rect.top - estimatedHeight - 6
      top = above >= 8 ? above : Math.max(8, safeBottom - estimatedHeight)
    }

    dropdownStyle.value = {
      position: 'fixed',
      top: `${top}px`,
      left: `${margin}px`,
      right: `${margin}px`,
      zIndex: 9999
    }
    return
  }

  const dropdownWidth = Math.max(rect.width, 220)

  let left = rect.left
  let top = rect.bottom + 6

  if (left + dropdownWidth > viewportWidth - 8) {
    left = viewportWidth - dropdownWidth - 8
  }

  const estimatedHeight = Math.min(320, viewportHeight * 0.6)
  if (top + estimatedHeight > viewportHeight - 8) {
    top = rect.top - estimatedHeight - 6
  }

  dropdownStyle.value = {
    position: 'fixed',
    top: `${top}px`,
    left: `${left}px`,
    width: `${dropdownWidth}px`,
    zIndex: 9999
  }
}

watch(open, (val) => {
  if (val) {
    nextTick(() => updateDropdownPosition())
  }
})

function handleResize() {
  if (open.value) {
    updateDropdownPosition()
  }
}

function selectAgent(agent) {
  emit('update:modelValue', agent)
  if (props.modelId != null) {
    emit('update:modelId', agent.agent_id || agent.id)
  }
  emit('select', agent)
  open.value = false
  searchQuery.value = ''
  statusFilter.value = ''
}

function selectAll() {
  if (props.modelId != null) {
    emit('update:modelId', props.allValue)
  }
  emit('select', null)
  open.value = false
  searchQuery.value = ''
  statusFilter.value = ''
}

function handleClickOutside(e) {
  if (
    pickerRef.value && !pickerRef.value.contains(e.target) &&
    dropdownRef.value && !dropdownRef.value.contains(e.target)
  ) {
    open.value = false
  }
}

onMounted(() => {
  document.addEventListener('mousedown', handleClickOutside)
  window.addEventListener('resize', handleResize)
})
onUnmounted(() => {
  document.removeEventListener('mousedown', handleClickOutside)
  window.removeEventListener('resize', handleResize)
})
</script>

<style scoped>
.agent-picker {
  position: relative;
  display: inline-block;
}
.agent-picker__trigger {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-subtle);
  border: var(--border-width-thick) solid var(--color-border-default);
  border-radius: var(--radius-lg);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  cursor: pointer;
  font-family: inherit;
  min-width: 200px;
}
.agent-picker__trigger:hover {
  border-color: var(--color-primary);
}
.agent-picker__dropdown {
  background: var(--color-bg-surface-raised);
  border: var(--border-width-thick) solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  overflow: hidden;
}
.agent-picker__search {
  padding: var(--space-2);
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
}
.agent-picker__search-input {
  width: 100%;
  padding: var(--space-1-5) var(--space-2-5);
  border: var(--border-width-thin) solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
}
.agent-picker__filters {
  display: flex;
  gap: var(--space-1);
  padding: var(--space-2);
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
  overflow-x: auto;
}
.agent-picker__filter-btn {
  padding: var(--space-1) var(--space-2-5);
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
  white-space: nowrap;
  font-family: inherit;
}
.agent-picker__filter-btn.active {
  background: var(--color-primary);
  color: var(--color-text-inverse);
}
.agent-picker__list {
  max-height: 240px;
  overflow-y: auto;
  padding: var(--space-1);
  scrollbar-width: thin;
}
.agent-picker__list::-webkit-scrollbar { width: 6px; }
.agent-picker__list::-webkit-scrollbar-track { background: transparent; }
.agent-picker__list::-webkit-scrollbar-thumb { background: var(--color-border-default); border-radius: var(--radius-full); }
.agent-picker__list::-webkit-scrollbar-thumb:hover { background: var(--color-text-muted); }
.agent-picker__item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2) var(--space-2-5);
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default);
  font-family: inherit;
  text-align: left;
}
.agent-picker__item:hover {
  background: var(--color-bg-hover);
}
.agent-picker__item--all {
  font-weight: var(--font-medium);
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
  border-radius: 0;
  margin: 0 var(--space-1);
  padding-left: var(--space-1-5);
  width: calc(100% - var(--space-2));
}
.agent-picker__item--all:hover {
  border-radius: var(--radius-md);
  margin: 0 var(--space-1);
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
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-picker__item-time {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}
.agent-picker__empty {
  padding: var(--space-4);
  text-align: center;
  font-size: var(--text-sm);
  color: var(--color-text-muted);
}
.agent-picker__sort {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  border-top: var(--border-width-thin) solid var(--color-border-subtle);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}
.agent-picker__sort-btn {
  padding: var(--space-0-5) var(--space-1-5);
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
  font-family: inherit;
}
.agent-picker__sort-btn.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: var(--font-medium);
}

@media (max-width: 640px) {
  .agent-picker__trigger {
    min-width: 140px;
    padding: var(--space-1-5) var(--space-2-5);
    font-size: var(--text-sm);
  }
  .agent-picker__dropdown {
    width: auto;
    min-width: auto;
    max-height: 70vh;
    max-height: 70dvh;
    display: flex;
    flex-direction: column;
  }
  .agent-picker__list {
    flex: 1;
    max-height: none;
  }
}
</style>
