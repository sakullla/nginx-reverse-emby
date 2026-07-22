<template>
  <div class="agent-metric-tile" :data-variant="variant" :data-display-mode="effectiveDisplayMode">
    <div class="agent-metric-tile__header">
      <i v-if="icon" :class="[icon, 'agent-metric-tile__icon']" />
      <span class="agent-metric-tile__label">{{ label }}</span>
    </div>

    <div v-if="hasNetwork" class="agent-metric-tile__network agent-metric-tile__network--stacked">
      <div class="agent-metric-tile__network-row" data-testid="agent-metric-tile-network-down" data-dir="down">
        <span class="agent-metric-tile__network-arrow" aria-hidden="true">↓</span>
        <span class="agent-metric-tile__network-value" :title="String(networkDownValue)">{{ networkDownValue }}</span>
      </div>
      <div class="agent-metric-tile__network-row" data-testid="agent-metric-tile-network-up" data-dir="up">
        <span class="agent-metric-tile__network-arrow" aria-hidden="true">↑</span>
        <span class="agent-metric-tile__network-value" :title="String(networkUpValue)">{{ networkUpValue }}</span>
      </div>
    </div>

    <div
      v-else-if="isRingMode"
      class="agent-metric-tile__ring"
      data-testid="agent-metric-tile-metric-ring"
      :data-tone="safeTone"
    >
      <div class="agent-metric-tile__ring-visual" aria-hidden="true">
        <svg class="agent-metric-tile__ring-svg" viewBox="0 0 36 36">
          <circle
            class="agent-metric-tile__ring-track"
            cx="18"
            cy="18"
            :r="RING_RADIUS"
            fill="none"
            :stroke-width="RING_STROKE"
          />
          <circle
            class="agent-metric-tile__ring-progress"
            data-testid="agent-metric-tile-ring-progress"
            cx="18"
            cy="18"
            :r="RING_RADIUS"
            fill="none"
            :stroke-width="RING_STROKE"
            :stroke-dasharray="ringDashArray"
            :stroke-dashoffset="ringDashOffset"
            stroke-linecap="round"
            transform="rotate(-90 18 18)"
          />
        </svg>
        <span class="agent-metric-tile__ring-percent" data-testid="agent-metric-tile-ring-percent">
          {{ ringPercentLabel }}
        </span>
      </div>
      <div class="agent-metric-tile__ring-meta">
        <span
          class="agent-metric-tile__ring-value"
          data-testid="agent-metric-tile-ring-value"
          :title="formattedDisplayValue"
        >
          {{ formattedDisplayValue }}
        </span>
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

const RING_RADIUS = 14
const RING_STROKE = 3.5
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS

const props = defineProps({
  icon: { type: String, default: '' },
  label: { type: String, required: true },
  value: { type: [String, Number], default: null },
  unit: { type: String, default: '' },
  percent: { type: Number, default: null },
  tone: { type: String, default: 'neutral' },
  variant: { type: String, default: 'default' },
  displayMode: {
    type: String,
    default: 'bar',
    validator: (v) => ['bar', 'ring'].includes(v),
  },
  networkDown: { type: [String, Number], default: null },
  networkUp: { type: [String, Number], default: null },
})

const hasNetwork = computed(() => props.networkDown != null || props.networkUp != null)

const isRingMode = computed(() => !hasNetwork.value && props.displayMode === 'ring')

const effectiveDisplayMode = computed(() => {
  if (hasNetwork.value) return 'network'
  return isRingMode.value ? 'ring' : 'bar'
})

const networkDownValue = computed(() => (props.networkDown == null ? '—' : props.networkDown))
const networkUpValue = computed(() => (props.networkUp == null ? '—' : props.networkUp))

const displayValue = computed(() => {
  if (props.value == null) return '—'
  return props.value
})

const formattedDisplayValue = computed(() => {
  if (props.value == null) return '—'
  const suffix = props.unit ? ` ${props.unit}` : ''
  return `${props.value}${suffix}`
})

const safeTone = computed(() =>
  ['success', 'warning', 'danger', 'neutral'].includes(props.tone) ? props.tone : 'neutral'
)

const hasPercent = computed(() => props.percent != null && Number.isFinite(Number(props.percent)))

const clampedPercent = computed(() => {
  if (!hasPercent.value) return 0
  return Math.min(100, Math.max(0, Number(props.percent)))
})

const ringPercentLabel = computed(() => {
  if (!hasPercent.value) return '—'
  const n = clampedPercent.value
  return Number.isInteger(n) ? `${n}%` : `${Number(n.toFixed(1))}%`
})

const ringDashArray = computed(() => `${RING_CIRCUMFERENCE} ${RING_CIRCUMFERENCE}`)

const ringDashOffset = computed(() => {
  const progress = hasPercent.value ? clampedPercent.value : 0
  return RING_CIRCUMFERENCE * (1 - progress / 100)
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
  flex-direction: column;
  align-items: stretch;
  gap: var(--space-0-5);
  min-width: 0;
}

.agent-metric-tile__network--stacked {
  flex-direction: column;
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

.agent-metric-tile__network-row[data-dir='down'] .agent-metric-tile__network-arrow {
  color: var(--color-success);
}

.agent-metric-tile__network-row[data-dir='up'] .agent-metric-tile__network-arrow {
  color: var(--color-primary);
}

.agent-metric-tile__network-value {
  font-size: var(--text-sm);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: visible;
  min-width: 0;
}

.agent-metric-tile__ring {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1-5);
  min-width: 0;
}

.agent-metric-tile__ring-meta {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-0-5);
  min-width: 0;
  max-width: 100%;
}

.agent-metric-tile__ring-visual {
  position: relative;
  width: 4.25rem;
  height: 4.25rem;
  flex-shrink: 0;
}

.agent-metric-tile__ring-svg {
  width: 100%;
  height: 100%;
  display: block;
}

.agent-metric-tile__ring-track {
  stroke: var(--color-bg-subtle);
}

.agent-metric-tile__ring-progress {
  transition: stroke-dashoffset var(--duration-slow) var(--ease-default),
    stroke var(--duration-fast) var(--ease-default);
}

.agent-metric-tile__ring[data-tone='success'] .agent-metric-tile__ring-progress {
  stroke: var(--color-success);
}

.agent-metric-tile__ring[data-tone='warning'] .agent-metric-tile__ring-progress {
  stroke: var(--color-warning);
}

.agent-metric-tile__ring[data-tone='danger'] .agent-metric-tile__ring-progress {
  stroke: var(--color-danger);
}

.agent-metric-tile__ring[data-tone='neutral'] .agent-metric-tile__ring-progress {
  stroke: var(--color-text-muted);
}

.agent-metric-tile__ring-percent {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--text-sm);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  line-height: 1;
  font-variant-numeric: tabular-nums;
}

.agent-metric-tile__ring-value {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
  line-height: 1.25;
  text-align: center;
  max-width: 100%;
  overflow-wrap: normal;
  word-break: normal;
  hyphens: none;
}

.agent-metric-tile[data-variant="compact"] {
  padding: var(--space-1-5) var(--space-2);
  gap: var(--space-1);
  min-height: 100%;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__header {
  padding-bottom: 0;
  border-bottom: none;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__label {
  font-size: 0.6875rem;
  letter-spacing: 0.02em;
  text-transform: uppercase;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__network {
  gap: 0.2rem;
  justify-content: center;
  flex: 1;
  padding: 0.1rem 0;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__network-row {
  gap: 0.3rem;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__network-value {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__ring {
  flex-direction: row;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-2);
  flex: 1;
  width: 100%;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__ring-meta {
  align-items: flex-start;
  flex: 1;
  min-width: 0;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__ring-visual {
  width: 2.75rem;
  height: 2.75rem;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__ring-percent {
  font-size: 0.625rem;
}

.agent-metric-tile[data-variant="compact"] .agent-metric-tile__ring-value {
  font-size: 0.6875rem;
  letter-spacing: -0.015em;
  text-align: left;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary);
  font-weight: var(--font-semibold);
}
</style>
