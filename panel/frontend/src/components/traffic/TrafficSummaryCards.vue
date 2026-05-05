<template>
  <div class="traffic-summary-cards">
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">已用</span>
      <span class="traffic-summary-card__value">{{ formatBytes(summary.used_bytes) }}</span>
      <span v-if="percent != null" class="traffic-summary-card__percent" :class="`traffic-summary-card__percent--${color}`">
        {{ percent }}%
      </span>
      <div v-if="percent != null" class="traffic-summary-card__track">
        <div class="traffic-summary-card__fill" :class="`traffic-summary-card__fill--${color}`" :style="{ width: `${percent}%` }" />
      </div>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">额度</span>
      <span class="traffic-summary-card__value">{{ formatQuota(summary.monthly_quota_bytes) }}</span>
      <span class="traffic-summary-card__sub">方向: {{ directionLabel }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">剩余 / 日均可用</span>
      <span class="traffic-summary-card__value">{{ remainingLabel }}</span>
      <span v-if="dailyBudgetText" class="traffic-summary-card__sub">{{ dailyBudgetText }}</span>
    </div>
    <div v-if="hasHostTotal" class="traffic-summary-card">
      <span class="traffic-summary-card__label">主机总计</span>
      <span class="traffic-summary-card__value">{{ formatBytes(hostTotal.accounted_bytes) }}</span>
      <span class="traffic-summary-card__sub">RX {{ formatBytes(hostTotal.rx_bytes) }} / TX {{ formatBytes(hostTotal.tx_bytes) }}</span>
    </div>
    <div class="traffic-summary-card" :class="{ 'traffic-summary-card--blocked': summary.blocked }">
      <span class="traffic-summary-card__label">状态</span>
      <span class="traffic-summary-card__value">{{ summary.blocked ? '已阻断' : '正常' }}</span>
      <span v-if="summary.blocked" class="traffic-summary-card__sub">超额阻断已生效</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, formatQuota, usagePercent, dailyBudget, quotaColorThreshold } from '../../utils/trafficStats.js'

const props = defineProps({
  summary: { type: Object, default: () => ({}) },
  direction: { type: String, default: 'both' },
  hostTotal: { type: Object, default: null }
})

const directionLabel = computed(() => {
  switch (String(props.direction || 'both').toLowerCase()) {
    case 'rx': return '入站'
    case 'tx': return '出站'
    case 'max': return '取最大值'
    default: return '双向'
  }
})

const percent = computed(() => usagePercent(props.summary.used_bytes, props.summary.monthly_quota_bytes))
const color = computed(() => quotaColorThreshold(percent.value))

const remainingLabel = computed(() => {
  if (props.summary.remaining_bytes == null) return '无限制'
  return formatBytes(props.summary.remaining_bytes)
})

const dailyBudgetText = computed(() => {
  const cycleStart = props.summary.cycle_start ? new Date(props.summary.cycle_start) : null
  const cycleEnd = props.summary.cycle_end ? new Date(props.summary.cycle_end) : null
  if (!cycleStart || !cycleEnd) return ''
  const days = Math.max(1, Math.ceil((cycleEnd - cycleStart) / 86400000))
  const budget = dailyBudget(props.summary.monthly_quota_bytes, days)
  if (budget == null) return ''
  return `日均 ${formatBytes(budget)}`
})

const hostTotal = computed(() => ({
  rx_bytes: Number(props.hostTotal?.rx_bytes) || 0,
  tx_bytes: Number(props.hostTotal?.tx_bytes) || 0,
  accounted_bytes: Number(props.hostTotal?.accounted_bytes) || 0
}))

const hasHostTotal = computed(() => {
  return hostTotal.value.rx_bytes > 0 || hostTotal.value.tx_bytes > 0 || hostTotal.value.accounted_bytes > 0
})
</script>

<style scoped>
.traffic-summary-cards {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 0.75rem;
  margin-bottom: 1rem;
}
.traffic-summary-card {
  min-width: 0;
  padding: 0.75rem;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
}
.traffic-summary-card--blocked {
  background: var(--color-danger-50);
  border-color: var(--color-danger-100);
}
.traffic-summary-card__label {
  display: block;
  margin-bottom: 0.25rem;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}
.traffic-summary-card__value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1.125rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.traffic-summary-card__percent {
  display: block;
  font-size: 0.875rem;
  font-weight: 600;
  margin-top: 0.25rem;
}
.traffic-summary-card__percent--success { color: var(--color-success); }
.traffic-summary-card__percent--warning { color: var(--color-warning); }
.traffic-summary-card__percent--danger { color: var(--color-danger); }
.traffic-summary-card__track {
  height: 4px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
  margin-top: 0.375rem;
}
.traffic-summary-card__fill {
  height: 100%;
  border-radius: var(--radius-full);
  transition: width 0.3s;
}
.traffic-summary-card__fill--success { background: var(--color-success); }
.traffic-summary-card__fill--warning { background: var(--color-warning); }
.traffic-summary-card__fill--danger { background: var(--color-danger); }
.traffic-summary-card__sub {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin-top: 0.25rem;
}
@media (max-width: 900px) {
  .traffic-summary-cards { grid-template-columns: repeat(3, 1fr); }
}
@media (max-width: 640px) {
  .traffic-summary-cards { grid-template-columns: repeat(2, 1fr); }
}
</style>
