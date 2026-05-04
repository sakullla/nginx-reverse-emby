<template>
  <div class="traffic-breakdown">
    <div
      v-for="row in rows"
      :key="rowKey(row)"
      class="traffic-breakdown__row"
      :class="{ 'traffic-breakdown__row--clickable': clickable }"
      @click="clickable && $emit('click-row', row)"
    >
      <span class="traffic-breakdown__name">{{ rowLabel(row) }}</span>
      <span class="traffic-breakdown__value">{{ formatBytes(row.accounted_bytes) }}</span>
      <span class="traffic-breakdown__raw">RX {{ formatBytes(row.rx_bytes) }} / TX {{ formatBytes(row.tx_bytes) }}</span>
    </div>
    <p v-if="rows.length === 0" class="traffic-breakdown__empty">暂无分项流量</p>
  </div>
</template>

<script setup>
import { formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  rows: { type: Array, default: () => [] },
  clickable: { type: Boolean, default: false }
})

defineEmits(['click-row'])

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
    default: return row.scope_id ? `${row.scope_type} #${row.scope_id}` : row.scope_type || '-'
  }
}
</script>

<style scoped>
.traffic-breakdown { display: flex; flex-direction: column; gap: 0.35rem; }
.traffic-breakdown__row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(10rem, auto);
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
.traffic-breakdown__empty { text-align: center; color: var(--color-text-muted); padding: 1.5rem; font-size: 0.875rem; margin: 0; }
@media (max-width: 720px) {
  .traffic-breakdown__row { grid-template-columns: 1fr auto; }
  .traffic-breakdown__raw { grid-column: 1 / -1; text-align: left; }
}
</style>
