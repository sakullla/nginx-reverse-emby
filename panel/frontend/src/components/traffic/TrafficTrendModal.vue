<template>
  <BaseModal v-model="visible" :title="`流量趋势 — ${scopeLabel}`" size="lg">
    <div class="traffic-trend-modal">
      <div class="traffic-trend-modal__controls">
        <div class="traffic-trend-modal__granularity">
          <button
            v-for="opt in granularityOptions"
            :key="opt.value"
            class="traffic-trend-modal__mode"
            :class="{ 'traffic-trend-modal__mode--active': granularity === opt.value }"
            type="button"
            @click="granularity = opt.value"
          >
            {{ opt.label }}
          </button>
        </div>
      </div>
      <div v-if="trendQuery.isLoading.value" class="traffic-trend-modal__loading">
        <div class="spinner"></div>
      </div>
      <div v-else-if="trendPoints.length > 0" class="traffic-trend-modal__chart">
        <TrafficTrendChart :points="trendPoints" :granularity="granularity" />
      </div>
      <div v-else class="traffic-trend-modal__empty">暂无趋势数据</div>
      <div v-if="summaryText" class="traffic-trend-modal__summary">{{ summaryText }}</div>
    </div>
  </BaseModal>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import BaseModal from '../base/BaseModal.vue'
import TrafficTrendChart from './TrafficTrendChart.vue'
import { useTrafficTrend } from '../../hooks/useTraffic.js'
import { normalizeTrafficTrendPoints, formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  visible: { type: Boolean, default: false },
  agentId: { type: String, default: '' },
  scopeType: { type: String, default: '' },
  scopeId: { type: String, default: '' },
  scopeLabel: { type: String, default: '' },
  direction: { type: String, default: 'both' }
})

const emit = defineEmits(['update:visible'])

const visible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const granularityOptions = [
  { value: 'hour', label: '小时' },
  { value: 'day', label: '日' },
  { value: 'month', label: '月' }
]
const granularity = ref('day')

const trendQuery = useTrafficTrend(
  computed(() => props.visible ? props.agentId : null),
  computed(() => ({
    granularity: granularity.value,
    scope_type: props.scopeType,
    scope_id: String(props.scopeId)
  }))
)

const trendPoints = computed(() => normalizeTrafficTrendPoints(trendQuery.data.value ?? [], props.direction))

const summaryText = computed(() => {
  const points = trendPoints.value
  if (!points.length) return ''
  const totalRx = points.reduce((sum, p) => sum + (Number(p.rx_bytes) || 0), 0)
  const totalTx = points.reduce((sum, p) => sum + (Number(p.tx_bytes) || 0), 0)
  return `合计  RX ${formatBytes(totalRx)}  TX ${formatBytes(totalTx)}`
})

watch(() => props.visible, (val) => {
  if (val) {
    granularity.value = 'day'
  }
})
</script>

<style scoped>
.traffic-trend-modal__controls {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 1rem;
}
.traffic-trend-modal__granularity {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
}
.traffic-trend-modal__mode {
  min-width: 2.75rem;
  padding: 0.3rem 0.55rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
  font-family: inherit;
}
.traffic-trend-modal__mode--active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}
.traffic-trend-modal__chart {
  min-height: 280px;
}
.traffic-trend-modal__loading {
  display: flex;
  justify-content: center;
  padding: 3rem;
}
.traffic-trend-modal__empty {
  text-align: center;
  color: var(--color-text-muted);
  padding: 3rem;
  font-size: 0.875rem;
}
.traffic-trend-modal__summary {
  margin-top: 0.75rem;
  padding-top: 0.75rem;
  border-top: 1px solid var(--color-border-subtle);
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  font-variant-numeric: tabular-nums;
}
.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
