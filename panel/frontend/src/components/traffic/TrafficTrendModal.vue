<template>
  <BaseModal v-model="visible" :title="`流量趋势 — ${scopeLabel}`" size="lg">
    <div class="traffic-trend-modal">
      <div class="traffic-trend-modal__toolbar">
        <div class="traffic-trend-modal__range">
          <input v-model="dateFrom" type="date" class="traffic-trend-modal__date-input">
          <span class="traffic-trend-modal__range-sep">—</span>
          <input v-model="dateTo" type="date" class="traffic-trend-modal__date-input">
        </div>
        <label class="traffic-trend-modal__compare">
          <input v-model="compareEnabled" type="checkbox">
          <span>对比上期</span>
        </label>
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
        <TrafficTrendChart
          :points="trendPoints"
          :prev-points="prevTrendPoints"
          :granularity="granularity"
          :quota-bytes="quotaBytes"
          :budget-bytes="budgetBytes"
        />
      </div>
      <div v-else class="traffic-trend-modal__empty">暂无趋势数据</div>
      <div v-if="stats.length" class="traffic-trend-modal__stats">
        <div v-for="s in stats" :key="s.label" class="traffic-trend-modal__stat">
          <span class="traffic-trend-modal__stat-label">{{ s.label }}</span>
          <span class="traffic-trend-modal__stat-value" :class="s.class">{{ s.value }}</span>
        </div>
      </div>
    </div>
  </BaseModal>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import BaseModal from '../base/BaseModal.vue'
import TrafficTrendChart from './TrafficTrendChart.vue'
import { useTrafficTrend } from '../../hooks/useTraffic.js'
import { normalizeTrafficTrendPoints, formatBytes, dailyBudget } from '../../utils/trafficStats.js'

const props = defineProps({
  visible: { type: Boolean, default: false },
  agentId: { type: String, default: '' },
  scopeType: { type: String, default: '' },
  scopeId: { type: String, default: '' },
  scopeLabel: { type: String, default: '' },
  direction: { type: String, default: 'both' },
  quotaBytes: { type: Number, default: null }
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
const dateFrom = ref('')
const dateTo = ref('')
const compareEnabled = ref(false)

function toISODate(d) {
  if (!d) return ''
  const date = new Date(d)
  if (Number.isNaN(date.getTime())) return ''
  return date.toISOString().slice(0, 10)
}

function prevRange(from, to) {
  const f = from ? new Date(from) : null
  const t = to ? new Date(to) : null
  if (!f || !t) return { from: '', to: '' }
  const duration = t.getTime() - f.getTime()
  const prevFrom = new Date(f.getTime() - duration - 1)
  const prevTo = new Date(f.getTime() - 1)
  return { from: toISODate(prevFrom), to: toISODate(prevTo) }
}

const trendParams = computed(() => ({
  granularity: granularity.value,
  from: dateFrom.value || undefined,
  to: dateTo.value || undefined,
  scope_type: props.scopeType,
  scope_id: String(props.scopeId)
}))

const prevParams = computed(() => {
  if (!compareEnabled.value) return null
  const { from, to } = prevRange(dateFrom.value, dateTo.value)
  if (!from || !to) return null
  return {
    granularity: granularity.value,
    from,
    to,
    scope_type: props.scopeType,
    scope_id: String(props.scopeId)
  }
})

const enabledAgentId = computed(() => props.visible ? props.agentId : null)

const trendQuery = useTrafficTrend(enabledAgentId, trendParams)
const prevTrendQuery = useTrafficTrend(enabledAgentId, prevParams)

const trendPoints = computed(() => normalizeTrafficTrendPoints(trendQuery.data.value ?? [], props.direction))
const prevTrendPoints = computed(() =>
  compareEnabled.value ? normalizeTrafficTrendPoints(prevTrendQuery.data.value ?? [], props.direction) : null
)

const totalAccounted = computed(() =>
  trendPoints.value.reduce((sum, p) => sum + (Number(p.accounted_bytes) || 0), 0)
)
const prevTotalAccounted = computed(() =>
  prevTrendPoints.value ? prevTrendPoints.value.reduce((sum, p) => sum + (Number(p.accounted_bytes) || 0), 0) : 0
)

const bucketCount = computed(() => trendPoints.value.length)
const dailyAvg = computed(() => {
  if (!bucketCount.value) return 0
  return Math.round(totalAccounted.value / bucketCount.value)
})

const momChange = computed(() => {
  if (!prevTotalAccounted.value) return null
  const change = ((totalAccounted.value - prevTotalAccounted.value) / prevTotalAccounted.value) * 100
  return Math.round(change * 10) / 10
})

const budgetBytes = computed(() => {
  if (!props.quotaBytes || granularity.value === 'month') return null
  return dailyBudget(props.quotaBytes, 30)
})

const stats = computed(() => {
  const items = []
  if (trendPoints.value.length) {
    items.push({ label: '当前合计', value: formatBytes(totalAccounted.value) })
    if (compareEnabled.value && prevTrendPoints.value?.length) {
      items.push({ label: '上期合计', value: formatBytes(prevTotalAccounted.value) })
      const mom = momChange.value
      const cls = mom != null && mom > 0 ? 'traffic-trend-modal__stat-value--up' : mom != null && mom < 0 ? 'traffic-trend-modal__stat-value--down' : ''
      items.push({ label: '环比', value: mom != null ? `${mom > 0 ? '+' : ''}${mom}%` : '—', class: cls })
    }
    if (bucketCount.value > 1) {
      items.push({ label: '日均', value: formatBytes(dailyAvg.value) })
    }
  }
  return items
})

watch(() => props.visible, (val) => {
  if (val) {
    granularity.value = 'day'
    dateFrom.value = ''
    dateTo.value = ''
    compareEnabled.value = false
  }
})
</script>

<style scoped>
.traffic-trend-modal__toolbar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 1rem;
  flex-wrap: wrap;
}
.traffic-trend-modal__range {
  display: flex;
  align-items: center;
  gap: 0.35rem;
}
.traffic-trend-modal__date-input {
  padding: 0.3rem 0.5rem;
  font-size: 0.8125rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-family: inherit;
}
.traffic-trend-modal__range-sep {
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
}
.traffic-trend-modal__compare {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.8125rem;
  color: var(--color-text-secondary);
  cursor: pointer;
  user-select: none;
}
.traffic-trend-modal__compare input {
  cursor: pointer;
}
.traffic-trend-modal__granularity {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  margin-left: auto;
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
.traffic-trend-modal__stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 0.75rem;
  margin-top: 0.75rem;
  padding-top: 0.75rem;
  border-top: 1px solid var(--color-border-subtle);
}
.traffic-trend-modal__stat {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}
.traffic-trend-modal__stat-label {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
.traffic-trend-modal__stat-value {
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}
.traffic-trend-modal__stat-value--up { color: var(--color-danger); }
.traffic-trend-modal__stat-value--down { color: var(--color-success); }
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
