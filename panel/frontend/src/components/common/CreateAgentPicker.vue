<template>
  <div
    v-if="visible"
    class="create-agent-picker-overlay"
    @click.self="onCancel"
  >
    <div class="create-agent-picker" role="dialog" aria-modal="true" aria-label="选择新建所属节点">
      <div class="create-agent-picker__header">
        <h3 class="create-agent-picker__title">选择新建所属节点</h3>
        <button
          type="button"
          class="create-agent-picker__close"
          aria-label="关闭"
          @click="onCancel"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>

      <p class="create-agent-picker__hint">全部节点视图下必须指定资源归属节点。</p>

      <div class="create-agent-picker__search">
        <svg
          class="create-agent-picker__search-icon"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          aria-hidden="true"
        >
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-3.5-3.5" />
        </svg>
        <input
          ref="searchInputRef"
          v-model="searchQuery"
          type="search"
          class="create-agent-picker__search-input"
          placeholder="搜索节点..."
          aria-label="搜索节点"
          @keydown.esc.prevent="onCancel"
        />
      </div>

      <div class="create-agent-picker__filters">
        <button
          v-for="opt in statusOptions"
          :key="opt.value"
          type="button"
          class="create-agent-picker__filter-btn"
          :class="`create-agent-picker__filter-btn--${opt.value || 'all'} ${statusFilter === opt.value ? 'create-agent-picker__filter-btn--active' : ''}`"
          @click="statusFilter = opt.value"
        >
          {{ opt.label }}
        </button>
      </div>

      <div class="create-agent-picker__list" role="listbox" aria-label="节点">
        <button
          v-for="agent in displayedAgents"
          :key="agent.id"
          type="button"
          class="create-agent-picker__item"
          role="option"
          @click="onSelect(agent)"
        >
          <span
            class="create-agent-picker__status-dot"
            :class="`create-agent-picker__status-dot--${getAgentStatus(agent)}`"
            aria-hidden="true"
          />
          <span class="create-agent-picker__name">{{ agent.name || agent.id }}</span>
          <span class="create-agent-picker__time">{{ timeAgo(agent.last_seen_at) }}</span>
        </button>

        <div v-if="!displayedAgents.length" class="create-agent-picker__empty">
          没有匹配的节点
        </div>
      </div>

      <div class="create-agent-picker__sort">
        <span>排序:</span>
        <button
          type="button"
          class="create-agent-picker__sort-btn"
          :class="{ 'create-agent-picker__sort-btn--active': sortBy === 'last_seen', 'create-agent-picker__sort-btn--last_seen': true }"
          @click="sortBy = 'last_seen'"
        >
          最近活跃
        </button>
        <button
          type="button"
          class="create-agent-picker__sort-btn"
          :class="{ 'create-agent-picker__sort-btn--active': sortBy === 'name', 'create-agent-picker__sort-btn--name': true }"
          @click="sortBy = 'name'"
        >
          名称
        </button>
      </div>

      <div class="create-agent-picker__actions">
        <button type="button" class="btn btn-secondary" @click="onCancel">取消</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import { getAgentStatus, timeAgo } from '../../utils/agentHelpers.js'

const props = defineProps({
  visible: { type: Boolean, default: false },
  agents: { type: Array, default: () => [] }
})

const emit = defineEmits(['select', 'cancel'])

const searchQuery = ref('')
const statusFilter = ref('')
const sortBy = ref('last_seen')
const searchInputRef = ref(null)

const statusOptions = [
  { value: '', label: '全部' },
  { value: 'online', label: '在线' },
  { value: 'offline', label: '离线' }
]

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

function onSelect(agent) {
  if (!agent?.id) return
  emit('select', agent)
  searchQuery.value = ''
  statusFilter.value = ''
  sortBy.value = 'last_seen'
}

function onCancel() {
  emit('cancel')
  searchQuery.value = ''
  statusFilter.value = ''
  sortBy.value = 'last_seen'
}

watch(() => props.visible, async (value) => {
  if (!value) return
  await nextTick()
  searchInputRef.value?.focus?.()
})
</script>

<style scoped>
.create-agent-picker-overlay {
  position: fixed;
  inset: 0;
  background: var(--color-overlay);
  backdrop-filter: blur(8px);
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
}

.create-agent-picker {
  background: var(--color-bg-surface-raised);
  border: var(--border-width-thick) solid var(--color-border-default);
  border-radius: var(--radius-3xl);
  width: min(420px, 90vw);
  max-height: min(520px, calc(100vh - var(--space-8)));
  max-height: min(520px, calc(100dvh - var(--space-8)));
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: var(--shadow-2xl);
}

.create-agent-picker__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4) var(--space-5);
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
}

.create-agent-picker__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  margin: 0;
  color: var(--color-text-primary);
}

.create-agent-picker__close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.75rem;
  height: 1.75rem;
  padding: 0;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.create-agent-picker__close:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.create-agent-picker__hint {
  margin: 0;
  padding: var(--space-3) var(--space-5) 0;
  font-size: var(--text-sm);
  color: var(--color-text-muted);
}

.create-agent-picker__search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: var(--space-3) var(--space-5);
  padding: 0 var(--space-3);
  min-height: 34px;
  border-radius: var(--radius-lg);
  border: var(--border-width-thick) solid var(--color-border-default);
  background: var(--color-bg-surface);
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.create-agent-picker__search:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.create-agent-picker__search-icon {
  flex-shrink: 0;
  color: var(--color-text-muted);
}

.create-agent-picker__search-input {
  flex: 1;
  min-width: 0;
  border: none;
  background: transparent;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  font-family: inherit;
  outline: none;
  padding: var(--space-1) 0;
}

.create-agent-picker__search-input::-webkit-search-cancel-button {
  appearance: none;
}

.create-agent-picker__filters {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 2rem;
  padding: var(--space-2) var(--space-5);
  background: var(--color-bg-surface-raised);
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
  overflow-x: auto;
}

.create-agent-picker__filter-btn {
  padding: var(--space-1-5) var(--space-4);
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  line-height: 1.4;
  cursor: pointer;
  white-space: nowrap;
  font-family: inherit;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.create-agent-picker__filter-btn:hover {
  background: var(--color-bg-hover);
}

.create-agent-picker__filter-btn--active {
  background: var(--color-primary);
  color: var(--color-text-inverse);
}

.create-agent-picker__list {
  flex: 0 1 auto;
  overflow-y: auto;
  max-height: 260px;
  padding: 0 var(--space-3) var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-height: 120px;
}

.create-agent-picker__item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: none;
  border-radius: var(--radius-lg);
  background: transparent;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default);
}

.create-agent-picker__item:hover {
  background: var(--color-bg-hover);
}

.create-agent-picker__item:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
}

.create-agent-picker__status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.create-agent-picker__status-dot--online {
  background: var(--color-success);
}

.create-agent-picker__status-dot--offline {
  background: var(--color-text-muted);
}

.create-agent-picker__status-dot--failed {
  background: var(--color-danger);
}

.create-agent-picker__status-dot--pending {
  background: var(--color-warning);
}

.create-agent-picker__name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.create-agent-picker__time {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.create-agent-picker__empty {
  padding: var(--space-8) 0;
  text-align: center;
  font-size: var(--text-sm);
  color: var(--color-text-muted);
}

.create-agent-picker__sort {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-5);
  border-top: var(--border-width-thin) solid var(--color-border-subtle);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

.create-agent-picker__sort-btn {
  padding: var(--space-0-5) var(--space-1-5);
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
  font-family: inherit;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.create-agent-picker__sort-btn:hover {
  background: var(--color-bg-hover);
}

.create-agent-picker__sort-btn--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: var(--font-medium);
}

.create-agent-picker__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-5) var(--space-4);
  border-top: var(--border-width-thin) solid var(--color-border-subtle);
}
</style>
