<template>
  <div class="traffic-bar" @click.stop="$emit('click')">
    <div class="traffic-bar__header">
      <span class="traffic-bar__label">用量 {{ formatBytes(accounted) }}</span>
      <span class="traffic-bar__percent" :class="`traffic-bar__percent--${color}`">
        {{ percentLabel }}
      </span>
    </div>
    <div class="traffic-bar__track">
      <div
        class="traffic-bar__fill"
        :class="`traffic-bar__fill--${color}`"
        :style="{ width: `${Math.min(100, percent)}%` }"
      />
    </div>
    <div class="traffic-bar__detail">
      <span>入 {{ formatBytes(rx) }}</span>
      <span>出 {{ formatBytes(tx) }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, usagePercent, quotaColorThreshold } from '../../utils/trafficStats.js'

const props = defineProps({
  accounted: { type: Number, default: 0 },
  rx: { type: Number, default: 0 },
  tx: { type: Number, default: 0 },
  nodeTotal: { type: Number, default: 0 }
})

defineEmits(['click'])

const percent = computed(() => {
  if (!props.nodeTotal || props.nodeTotal <= 0) return 0
  return Math.round((props.accounted / props.nodeTotal) * 100)
})

const color = computed(() => quotaColorThreshold(percent.value))

const percentLabel = computed(() => {
  if (!props.nodeTotal || props.nodeTotal <= 0) return ''
  return `占节点 ${percent.value}%`
})
</script>

<style scoped>
.traffic-bar {
  background: color-mix(in srgb, var(--color-bg-subtle) 82%, transparent);
  border: 1px solid color-mix(in srgb, var(--color-border-subtle) 85%, transparent);
  border-radius: var(--radius-lg);
  padding: 0.45rem 0.6rem;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}
.traffic-bar:hover {
  background: var(--color-bg-hover);
  border-color: var(--color-border-default);
}
.traffic-bar__header {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 0.5rem;
  margin-bottom: 0.2rem;
  font-size: 0.75rem;
}
.traffic-bar__label {
  font-weight: 650;
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
  min-width: 0;
}
.traffic-bar__percent {
  flex-shrink: 0;
  font-size: 0.6875rem;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}
.traffic-bar__percent--success { color: var(--color-success); }
.traffic-bar__percent--warning { color: var(--color-warning); }
.traffic-bar__percent--danger { color: var(--color-danger); }
.traffic-bar__track {
  height: 3px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
}
.traffic-bar__fill {
  height: 100%;
  border-radius: var(--radius-full);
  transition: width 0.3s;
  min-width: 2px;
}
.traffic-bar__fill--success { background: var(--color-success); }
.traffic-bar__fill--warning { background: var(--color-warning); }
.traffic-bar__fill--danger { background: var(--color-danger); }
.traffic-bar__detail {
  display: flex;
  justify-content: space-between;
  gap: 0.5rem;
  margin-top: 0.2rem;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
}
</style>
