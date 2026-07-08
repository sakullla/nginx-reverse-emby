<template>
  <div class="agent-metric-tile" :data-variant="variant">
    <div class="agent-metric-tile__header">
      <i v-if="icon" :class="[icon, 'agent-metric-tile__icon']" />
      <span class="agent-metric-tile__label">{{ label }}</span>
    </div>

    <div v-if="hasNetwork" class="agent-metric-tile__network">
      <div class="agent-metric-tile__network-row" data-testid="agent-metric-tile-network-down">
        <span class="agent-metric-tile__network-arrow">↓</span>
        <span class="agent-metric-tile__network-value">{{ networkDownValue }}</span>
      </div>
      <div class="agent-metric-tile__network-row" data-testid="agent-metric-tile-network-up">
        <span class="agent-metric-tile__network-arrow">↑</span>
        <span class="agent-metric-tile__network-value">{{ networkUpValue }}</span>
      </div>
    </div>

    <BaseMetricBar
      v-else
      :label="''"
      :value="displayValue"
      :unit="unit"
      :percent="percent"
      :tone="tone"
      class="agent-metric-tile__metric-bar"
      data-testid="agent-metric-tile-metric-bar"
    />
  </div>
</template>

<script setup>
import { computed } from 'vue'
import BaseMetricBar from './base/BaseMetricBar.vue'

const props = defineProps({
  icon: { type: String, default: '' },
  label: { type: String, required: true },
  value: { type: [String, Number], default: null },
  unit: { type: String, default: '' },
  percent: { type: Number, default: null },
  tone: { type: String, default: 'neutral' },
  variant: { type: String, default: 'default' },
  networkDown: { type: [String, Number], default: null },
  networkUp: { type: [String, Number], default: null },
})

const hasNetwork = computed(() => props.networkDown != null || props.networkUp != null)

const networkDownValue = computed(() => (props.networkDown == null ? '—' : props.networkDown))
const networkUpValue = computed(() => (props.networkUp == null ? '—' : props.networkUp))

const displayValue = computed(() => {
  if (props.value == null) return '—'
  return props.value
})
</script>

<style scoped>
.agent-metric-tile {
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
  min-width: 0;
  padding: var(--space-2-5) var(--space-3);
  border: 1px solid var(--color-dashboard-tile-border);
  border-radius: var(--radius-md);
  background: var(--color-dashboard-tile-bg);
  box-shadow: var(--color-dashboard-tile-shadow);
  transition: background-color var(--duration-fast) var(--ease-default),
    border-color var(--duration-fast) var(--ease-default),
    box-shadow var(--duration-fast) var(--ease-default);
}

.agent-metric-tile__header {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
  padding-bottom: var(--space-1);
  border-bottom: 1px solid var(--color-dashboard-tile-header-bg);
}

.agent-metric-tile__icon {
  width: 0.875rem;
  height: 0.875rem;
  color: var(--color-text-secondary);
  flex-shrink: 0;
}

.agent-metric-tile__label {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
  line-height: 1;
}

.agent-metric-tile__network {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.agent-metric-tile__network-row {
  display: flex;
  align-items: baseline;
  gap: var(--space-1);
  min-width: 0;
}

.agent-metric-tile__network-arrow {
  font-size: var(--text-xs);
  font-weight: var(--font-bold);
  color: var(--color-text-muted);
  line-height: 1;
  flex-shrink: 0;
}

.agent-metric-tile__network-value {
  font-size: var(--text-sm);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.agent-metric-tile[data-variant="compact"] {
  padding: var(--space-2) var(--space-2-5);
  gap: var(--space-1);
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__header {
  padding-bottom: var(--space-0-5);
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__network-value {
  font-size: var(--text-xs);
}
</style>
