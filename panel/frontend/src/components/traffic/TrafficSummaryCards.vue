<template>
  <div class="traffic-summary-cards">
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">已用</span>
      <span class="traffic-summary-card__value">{{ formatBytes(summary.used_bytes) }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">月额度</span>
      <span class="traffic-summary-card__value">{{ formatQuota(summary.monthly_quota_bytes) }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">剩余</span>
      <span class="traffic-summary-card__value">{{ summary.remaining_bytes == null ? '无限制' : formatBytes(summary.remaining_bytes) }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">周期</span>
      <span class="traffic-summary-card__value">{{ summary.cycle_start ? formatCycle(summary.cycle_start, summary.cycle_end) : '—' }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">计费方向</span>
      <span class="traffic-summary-card__value">{{ directionLabel }}</span>
    </div>
    <div class="traffic-summary-card" :class="{ 'traffic-summary-card--blocked': summary.blocked }">
      <span class="traffic-summary-card__label">状态</span>
      <span class="traffic-summary-card__value">{{ summary.blocked ? '已阻断' : '正常' }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, formatQuota } from '../../utils/trafficStats.js'

const props = defineProps({
  summary: { type: Object, default: () => ({}) },
  direction: { type: String, default: 'both' }
})

const directionLabel = computed(() => {
  switch (String(props.direction || 'both').toLowerCase()) {
    case 'rx': return '入站'
    case 'tx': return '出站'
    case 'max': return '取最大值'
    default: return '双向'
  }
})

function formatCycle(start, end) {
  if (!start || !end) return '—'
  return `${new Date(start).toLocaleDateString()} - ${new Date(end).toLocaleDateString()}`
}
</script>

<style scoped>
.traffic-summary-cards {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.75rem;
  margin-bottom: 1rem;
}
.traffic-summary-card {
  min-width: 0;
  padding: 0.75rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
}
.traffic-summary-card--blocked {
  background: var(--color-danger-50);
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
@media (max-width: 720px) {
  .traffic-summary-cards { grid-template-columns: repeat(2, 1fr); }
}
</style>
