<template>
  <ResourceListFilterBar
    class="agent-filter-bar"
    :show-agent-select="false"
    :q="searchQuery"
    search-placeholder="搜索节点名称 / IP / 标签 / #id=..."
    :filter-fields="filterFields"
    :filter-values="filterValues"
    @update:q="value => emit('update:searchQuery', value)"
    @update:filter="onFilterUpdate"
  >
    <template #trailing>
      <!-- Sort: field + direction -->
      <div class="sort-control">
        <BaseSelect
          v-model="sortField"
          class="sort-select"
          :options="SORT_OPTIONS"
          aria-label="排序字段"
          align="end"
        />
        <button
          class="sort-order-btn"
          :title="sortOrder === 'asc' ? '切换为降序' : '切换为升序'"
          :aria-label="sortOrder === 'asc' ? '当前升序，切换为降序' : '当前降序，切换为升序'"
          :aria-pressed="sortOrder === 'asc'"
          @click="toggleSortOrder"
        >
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

      <!-- View Toggle -->
      <div class="view-toggle">
        <button
          class="view-toggle__btn"
          :class="{ active: view === 'monitor' }"
          title="监控视图"
          aria-label="监控视图"
          :aria-pressed="view === 'monitor'"
          @click="view = 'monitor'"
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
          aria-label="列表视图"
          :aria-pressed="view === 'list'"
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
    </template>
  </ResourceListFilterBar>
</template>

<script setup>
import { computed } from 'vue'
import ResourceListFilterBar from './common/ResourceListFilterBar.vue'
import BaseSelect from './base/BaseSelect.vue'

const props = defineProps({
  view: { type: String, required: true },
  searchQuery: { type: String, default: '' },
  statusFilter: { type: String, required: true },
  modeFilter: { type: String, required: true },
  tagFilter: { type: String, required: true },
  sortField: { type: String, required: true },
  sortOrder: { type: String, required: true },
  availableTags: { type: Array, default: () => [] },
  hasActiveFilters: { type: Boolean, default: false }
})

const emit = defineEmits([
  'update:view', 'update:searchQuery', 'update:statusFilter', 'update:modeFilter', 'update:tagFilter',
  'update:sortField', 'update:sortOrder', 'clear-filters', 'toggle-sort-order'
])

const SORT_OPTIONS = [
  { value: 'last_seen_at', label: '最后活跃' },
  { value: 'name', label: '名称' },
  { value: 'http_rules_count', label: 'HTTP 规则' },
  { value: 'l4_rules_count', label: 'L4 规则' }
]

const STATUS_OPTIONS = [
  { value: '', label: '全部' },
  { value: 'online', label: '在线' },
  { value: 'offline', label: '离线' },
  { value: 'failed', label: '失败' },
  { value: 'pending', label: '同步中' }
]

const MODE_OPTIONS = [
  { value: '', label: '全部' },
  { value: 'local', label: '本机' },
  { value: 'master', label: '主控' },
  { value: 'pull', label: '拉取' }
]

const filterFields = computed(() => {
  const fields = [
    { key: 'status', label: '状态', type: 'chip', defaultValue: '', options: STATUS_OPTIONS },
    { key: 'mode', label: '模式', type: 'chip', defaultValue: '', options: MODE_OPTIONS }
  ]
  // Keep a stale tag filter removable even when no agent carries it anymore.
  const tags = [...props.availableTags]
  if (props.tagFilter && !tags.includes(props.tagFilter)) tags.push(props.tagFilter)
  if (tags.length) {
    fields.push({
      key: 'tag',
      label: '标签',
      type: 'chip',
      defaultValue: '',
      options: [{ value: '', label: '全部' }, ...tags.map((tag) => ({ value: tag, label: tag }))]
    })
  }
  return fields
})

const filterValues = computed(() => ({
  status: props.statusFilter,
  mode: props.modeFilter,
  tag: props.tagFilter
}))

const FILTER_EVENTS = {
  status: 'update:statusFilter',
  mode: 'update:modeFilter',
  tag: 'update:tagFilter'
}

function onFilterUpdate({ key, value }) {
  const event = FILTER_EVENTS[key]
  if (event) emit(event, String(value ?? ''))
}

const view = computed({
  get: () => props.view,
  set: (v) => emit('update:view', v)
})
const sortField = computed({
  get: () => props.sortField,
  set: (v) => emit('update:sortField', v)
})

function toggleSortOrder() {
  emit('toggle-sort-order')
}
</script>

<style scoped>
.view-toggle {
  display: flex;
  gap: 2px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-full);
  padding: 3px;
  border: 1px solid var(--color-border-subtle);
}
.view-toggle__btn {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-full);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}
/* ≥40px touch targets via an expanded pseudo-element hit area. */
.view-toggle__btn::after,
.sort-order-btn::after {
  content: '';
  position: absolute;
  inset: -5px;
  border-radius: var(--radius-full);
}
.view-toggle__btn:focus-visible,
.sort-order-btn:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
}
.view-toggle__btn:hover {
  color: var(--color-text-primary);
}
.view-toggle__btn.active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}
.sort-select {
  min-width: 8.5rem;
}
.sort-control {
  display: flex;
  align-items: center;
  gap: 4px;
}
.sort-order-btn {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: var(--radius-lg);
  border: var(--border-width-thick, 1px) solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}
.sort-order-btn:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
}
@media (max-width: 768px) {
  .sort-control {
    flex: 1 1 auto;
    min-width: 0;
  }
  .sort-control .sort-select {
    flex: 1 1 auto;
    min-width: 0;
  }
  .sort-order-btn {
    width: 44px;
    height: 44px;
    flex-shrink: 0;
  }
  .view-toggle__btn {
    width: 36px;
    height: 36px;
  }
}
</style>
