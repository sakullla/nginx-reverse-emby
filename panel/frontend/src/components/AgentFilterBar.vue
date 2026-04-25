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
import { computed } from 'vue'

const props = defineProps({
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
