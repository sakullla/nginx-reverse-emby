<template>
  <div class="traffic-summary-cards">
    <div class="traffic-summary-cards__grid">
      <div class="traffic-summary-card__metric traffic-summary-card__metric--primary">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10"/>
            <path d="M12 2a10 10 0 0 1 10 10H12V2z"/>
          </svg>
          <span class="traffic-summary-card__label">总流量</span>
        </div>
        <span class="traffic-summary-card__value">{{ formatBytes(summary.used_bytes) }}</span>
        <span v-if="percent != null" class="traffic-summary-card__sub">占额度 {{ percent }}%</span>
      </div>
      <div class="traffic-summary-card__metric">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="19" x2="12" y2="5"/>
            <polyline points="5 12 12 5 19 12"/>
          </svg>
          <span class="traffic-summary-card__label">上行</span>
        </div>
        <span class="traffic-summary-card__value">{{ formatBytes(summary.tx_bytes) }}</span>
      </div>
      <div class="traffic-summary-card__metric">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="5" x2="12" y2="19"/>
            <polyline points="19 12 12 19 5 12"/>
          </svg>
          <span class="traffic-summary-card__label">下行</span>
        </div>
        <span class="traffic-summary-card__value">{{ formatBytes(summary.rx_bytes) }}</span>
      </div>
      <div class="traffic-summary-card__metric traffic-summary-card__metric--primary">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
          </svg>
          <span class="traffic-summary-card__label">当前速率</span>
        </div>
        <span class="traffic-summary-card__value">{{ currentRate }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, usagePercent } from '../../utils/trafficStats.js'
import { rate } from '../../utils/agentMetrics.js'

const props = defineProps({
  summary: { type: Object, default: () => ({}) },
  direction: { type: String, default: 'both' },
  networkMetrics: { type: Object, default: null }
})

const percent = computed(() => usagePercent(props.summary.used_bytes, props.summary.monthly_quota_bytes))

const currentRate = computed(() => {
  const rx = props.networkMetrics?.rx_bytes_per_second
  const tx = props.networkMetrics?.tx_bytes_per_second
  const hasRx = rx != null && rx !== ''
  const hasTx = tx != null && tx !== ''
  if (!hasRx && !hasTx) return '—'
  if (hasRx && hasTx) return `↓ ${rate(rx)} ↑ ${rate(tx)}`
  if (hasRx) return `↓ ${rate(rx)}`
  return `↑ ${rate(tx)}`
})
</script>

<style scoped>
.traffic-summary-cards {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 1rem;
}
.traffic-summary-cards__grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 0.75rem;
}
.traffic-summary-card__metric {
  min-width: 0;
  text-align: left;
  padding: 0.5rem;
  border-radius: var(--radius-md);
  transition: background-color 0.15s ease;
}
.traffic-summary-card__metric:hover {
  background: var(--color-bg-subtle);
}
.traffic-summary-card__header {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  margin-bottom: 0.375rem;
}
.traffic-summary-card__icon {
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}
.traffic-summary-card__metric--primary .traffic-summary-card__icon {
  color: var(--color-primary);
}
.traffic-summary-card__label {
  display: block;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}
.traffic-summary-card__value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.3;
}
.traffic-summary-card__metric--primary .traffic-summary-card__value {
  font-size: 1.25rem;
  color: var(--color-text-primary);
}
.traffic-summary-card__sub {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-muted);
  margin-top: 0.25rem;
}
@media (max-width: 900px) {
  .traffic-summary-cards__grid { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 480px) {
  .traffic-summary-cards__grid { grid-template-columns: 1fr; }
}
</style>
