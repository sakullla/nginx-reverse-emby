<template>
  <div class="traffic-breakdown">
    <div v-if="computedTabs.length > 1" class="traffic-breakdown__tabs">
      <button
        v-for="tab in computedTabs"
        :key="tab.id"
        class="traffic-breakdown__tab"
        :class="{ 'traffic-breakdown__tab--active': activeTabId === tab.id }"
        @click="activeTabId = tab.id"
      >
        {{ tab.label }}
        <span v-if="tab.rows.length" class="traffic-breakdown__tab-count">{{ tab.rows.length }}</span>
      </button>
    </div>
    <div class="traffic-breakdown__table-header">
      <span class="traffic-breakdown__th traffic-breakdown__th--name">名称</span>
      <span class="traffic-breakdown__th traffic-breakdown__th--sortable" @click="setSort('accounted_bytes')">
        用量 {{ sortIcon('accounted_bytes') }}
      </span>
      <span class="traffic-breakdown__th traffic-breakdown__th--sortable" @click="setSort('rx_bytes')">
        RX {{ sortIcon('rx_bytes') }}
      </span>
      <span class="traffic-breakdown__th traffic-breakdown__th--sortable" @click="setSort('tx_bytes')">
        TX {{ sortIcon('tx_bytes') }}
      </span>
      <span class="traffic-breakdown__th">占比</span>
    </div>
    <div
      v-for="row in paginatedRows"
      :key="rowKey(row)"
      class="traffic-breakdown__row"
      :class="{ 'traffic-breakdown__row--clickable': clickable }"
      @click="clickable && $emit('click-row', row)"
    >
      <span class="traffic-breakdown__name">{{ rowLabel(row) }}</span>
      <span class="traffic-breakdown__value">{{ formatBytes(row.accounted_bytes) }}</span>
      <span class="traffic-breakdown__raw">{{ formatBytes(row.rx_bytes) }}</span>
      <span class="traffic-breakdown__raw">{{ formatBytes(row.tx_bytes) }}</span>
      <span class="traffic-breakdown__percent">{{ rowPercent(row) }}</span>
    </div>
    <p v-if="sortedRows.length === 0" class="traffic-breakdown__empty">暂无分项流量</p>
    <div v-if="totalPages > 1" class="traffic-breakdown__pagination">
      <button
        class="traffic-breakdown__page-btn"
        :disabled="currentPage <= 1"
        @click="currentPage--"
      >
        ←
      </button>
      <span class="traffic-breakdown__page-info">{{ currentPage }} / {{ totalPages }}</span>
      <button
        class="traffic-breakdown__page-btn"
        :disabled="currentPage >= totalPages"
        @click="currentPage++"
      >
        →
      </button>
      <select v-model="pageSize" class="traffic-breakdown__page-size">
        <option :value="10">10 条/页</option>
        <option :value="20">20 条/页</option>
        <option :value="50">50 条/页</option>
      </select>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  rows: { type: Array, default: () => [] },
  tabs: { type: Array, default: null },
  clickable: { type: Boolean, default: false }
})

defineEmits(['click-row'])

const activeTabId = ref('')
const sortKey = ref('accounted_bytes')
const sortAsc = ref(false)
const currentPage = ref(1)
const pageSize = ref(10)

const computedTabs = computed(() => {
  if (Array.isArray(props.tabs) && props.tabs.length > 0) {
    return props.tabs
  }
  return [{ id: 'all', label: '全部', rows: props.rows || [] }]
})

const currentTab = computed(() => {
  const found = computedTabs.value.find((t) => t.id === activeTabId.value)
  if (found) return found
  activeTabId.value = computedTabs.value[0]?.id || ''
  return computedTabs.value[0]
})

const currentRows = computed(() => currentTab.value?.rows || [])

const tabTotal = computed(() => {
  return currentRows.value.reduce((sum, r) => sum + (r.accounted_bytes || 0), 0)
})

const sortedRows = computed(() => {
  const key = sortKey.value
  const rows = [...currentRows.value]
  rows.sort((a, b) => {
    const av = a[key] || 0
    const bv = b[key] || 0
    return sortAsc.value ? av - bv : bv - av
  })
  return rows
})

const totalPages = computed(() => Math.max(1, Math.ceil(sortedRows.value.length / pageSize.value)))

const paginatedRows = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return sortedRows.value.slice(start, start + pageSize.value)
})

watch(() => currentTab.value?.id, () => { currentPage.value = 1 })
watch(() => [sortKey.value, sortAsc.value], () => { currentPage.value = 1 })

function setSort(key) {
  if (sortKey.value === key) {
    sortAsc.value = !sortAsc.value
  } else {
    sortKey.value = key
    sortAsc.value = false
  }
}

function sortIcon(key) {
  if (sortKey.value !== key) return '⇅'
  return sortAsc.value ? '↑' : '↓'
}

function rowKey(row) {
  return `${row.scope_type || 'scope'}-${row.scope_id || 'aggregate'}`
}

function rowLabel(row) {
  switch (row.scope_type) {
    case 'http': return 'HTTP'
    case 'l4': return 'L4'
    case 'relay': return 'Relay'
    case 'http_rule': return `HTTP 规则 #${row.scope_id}`
    case 'l4_rule': return `L4 规则 #${row.scope_id}`
    case 'relay_listener': return `Relay 监听 #${row.scope_id}`
    case 'host_total': return '主机总计'
    case 'host_interface': return `接口 ${row.scope_id}`
    default: return row.scope_id ? `${row.scope_type} #${row.scope_id}` : row.scope_type || '-'
  }
}

function rowPercent(row) {
  const total = tabTotal.value
  if (!total) return '—'
  const val = row.accounted_bytes || 0
  const pct = Math.round((val / total) * 100)
  return pct < 1 ? '<1%' : `${pct}%`
}
</script>

<style scoped>
.traffic-breakdown { display: flex; flex-direction: column; gap: 0.35rem; }
.traffic-breakdown__tabs {
  display: flex;
  gap: 0.25rem;
  margin-bottom: 0.5rem;
  flex-wrap: wrap;
}
.traffic-breakdown__tab {
  padding: 0.35rem 0.625rem;
  font-size: 0.8125rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all 0.15s;
}
.traffic-breakdown__tab:hover { background: var(--color-bg-hover); }
.traffic-breakdown__tab--active {
  background: var(--color-primary-50);
  border-color: var(--color-primary-100);
  color: var(--color-primary);
  font-weight: 600;
}
.traffic-breakdown__tab-count {
  margin-left: 0.25rem;
  font-size: 0.75rem;
  color: var(--color-text-muted);
}
.traffic-breakdown__tab--active .traffic-breakdown__tab-count { color: var(--color-primary); }
.traffic-breakdown__table-header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto auto auto;
  gap: 0.75rem;
  align-items: center;
  padding: 0.4rem 0.65rem;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  font-weight: 600;
}
.traffic-breakdown__th--sortable { cursor: pointer; user-select: none; }
.traffic-breakdown__th--sortable:hover { color: var(--color-text-primary); }
.traffic-breakdown__row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto auto auto;
  gap: 0.75rem;
  align-items: center;
  padding: 0.55rem 0.65rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
  font-size: 0.8125rem;
}
.traffic-breakdown__row--clickable { cursor: pointer; }
.traffic-breakdown__row--clickable:hover { background: var(--color-bg-hover); }
.traffic-breakdown__name {
  min-width: 0;
  color: var(--color-text-primary);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.traffic-breakdown__value { color: var(--color-text-primary); font-weight: 700; font-variant-numeric: tabular-nums; white-space: nowrap; }
.traffic-breakdown__raw { color: var(--color-text-tertiary); font-family: var(--font-mono); font-size: 0.75rem; text-align: right; white-space: nowrap; }
.traffic-breakdown__percent { color: var(--color-text-muted); font-size: 0.75rem; text-align: right; white-space: nowrap; min-width: 2.5rem; }
.traffic-breakdown__empty { text-align: center; color: var(--color-text-muted); padding: 1.5rem; font-size: 0.875rem; margin: 0; }
.traffic-breakdown__pagination {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
  padding: 0.5rem 0.65rem;
  font-size: 0.8125rem;
}
.traffic-breakdown__page-btn {
  padding: 0.3rem 0.55rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  cursor: pointer;
  transition: all 0.15s;
}
.traffic-breakdown__page-btn:hover:not(:disabled) { background: var(--color-bg-hover); }
.traffic-breakdown__page-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.traffic-breakdown__page-info {
  color: var(--color-text-secondary);
  font-weight: 600;
  min-width: 3rem;
  text-align: center;
}
.traffic-breakdown__page-size {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.75rem;
  font-family: inherit;
  cursor: pointer;
}
@media (max-width: 720px) {
  .traffic-breakdown__table-header { display: none; }
  .traffic-breakdown__row { grid-template-columns: 1fr auto; }
  .traffic-breakdown__raw,
  .traffic-breakdown__percent { grid-column: 1 / -1; text-align: left; }
}
</style>
