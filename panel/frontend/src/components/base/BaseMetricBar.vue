<template>
  <div class="base-metric-bar" :data-tone="safeTone">
    <div class="base-metric-bar__header">
      <span v-if="label" class="base-metric-bar__label">{{ label }}</span>
      <div class="base-metric-bar__meta">
        <span v-if="value != null" class="base-metric-bar__value">{{ formattedValue }}</span>
        <span v-if="hasPercent" class="base-metric-bar__percent">{{ percentLabel }}</span>
      </div>
    </div>
    <div class="base-metric-bar__track">
      <div
        class="base-metric-bar__fill"
        :class="`base-metric-bar__fill--${safeTone}`"
        :style="{ width: `${clampedPercent}%` }"
      />
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  label: { type: String, required: true },
  value: { type: [String, Number], default: null },
  unit: { type: String, default: '' },
  percent: { type: Number, default: 0 },
  tone: {
    type: String,
    default: 'neutral',
    validator: (v) => ['success', 'warning', 'danger', 'neutral'].includes(v),
  },
})

const safeTone = computed(() =>
  ['success', 'warning', 'danger', 'neutral'].includes(props.tone) ? props.tone : 'neutral'
)

const hasPercent = computed(() => props.percent != null && Number.isFinite(Number(props.percent)))

const clampedPercent = computed(() => {
  if (!hasPercent.value) return 0
  return Math.min(100, Math.max(0, Number(props.percent)))
})

const percentLabel = computed(() => {
  if (!hasPercent.value) return ''
  const n = clampedPercent.value
  return Number.isInteger(n) ? `${n}%` : `${Number(n.toFixed(1))}%`
})

const formattedValue = computed(() => {
  if (props.value == null) return ''
  const suffix = props.unit ? ` ${props.unit}` : ''
  return `${props.value}${suffix}`
})
</script>

<style scoped>
.base-metric-bar {
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
  min-width: 0;
}

.base-metric-bar__header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
  min-width: 0;
}

.base-metric-bar__label {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
  line-height: 1;
  flex-shrink: 0;
}

.base-metric-bar__meta {
  display: flex;
  align-items: baseline;
  justify-content: flex-end;
  gap: 0.5rem;
  min-width: 0;
  flex: 1;
}

.base-metric-bar__value {
  font-size: var(--text-sm);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  line-height: 1.2;
  overflow-wrap: anywhere;
  text-align: right;
  min-width: 0;
}

.base-metric-bar__percent {
  flex-shrink: 0;
  font-size: var(--text-xs);
  font-weight: 650;
  color: var(--color-text-secondary);
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
}

.base-metric-bar__track {
  height: 6px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-full);
  overflow: hidden;
}

.base-metric-bar__fill {
  height: 100%;
  border-radius: var(--radius-full);
  transition: width var(--duration-slow) var(--ease-default),
    background-color var(--duration-fast) var(--ease-default);
  min-width: 2px;
}

.base-metric-bar__fill--success {
  background: var(--color-success);
}

.base-metric-bar__fill--warning {
  background: var(--color-warning);
}

.base-metric-bar__fill--danger {
  background: var(--color-danger);
}

.base-metric-bar__fill--neutral {
  background: var(--color-text-muted);
}
</style>
