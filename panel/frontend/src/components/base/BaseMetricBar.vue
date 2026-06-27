<template>
  <div class="base-metric-bar" :data-tone="safeTone">
    <div class="base-metric-bar__header">
      <span class="base-metric-bar__label">{{ label }}</span>
      <span v-if="value != null" class="base-metric-bar__value">{{ formattedValue }}</span>
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

const clampedPercent = computed(() => {
  if (props.percent == null || Number.isNaN(props.percent)) return 0
  return Math.min(100, Math.max(0, props.percent))
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

.base-metric-bar__value {
  font-size: var(--text-sm);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  line-height: 1.2;
  overflow-wrap: anywhere;
  text-align: right;
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
