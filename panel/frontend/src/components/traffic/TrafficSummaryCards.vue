<template>
  <div class="traffic-summary-cards" :class="{ 'traffic-summary-cards--loading': showLoading }">
    <div
      v-if="showLoading"
      class="traffic-summary-cards__loading"
      data-testid="traffic-summary-loading"
      role="status"
      aria-live="polite"
    >
      加载中…
    </div>
    <div v-else class="traffic-summary-cards__grid">
      <div class="traffic-summary-card__metric traffic-summary-card__metric--primary">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10"/>
            <path d="M12 2a10 10 0 0 1 10 10H12V2z"/>
          </svg>
          <span class="traffic-summary-card__label">总流量</span>
        </div>
        <span class="traffic-summary-card__value" data-testid="traffic-summary-used">{{ formatBytes(summary.used_bytes) }}</span>
        <span v-if="usedSub" class="traffic-summary-card__sub" data-testid="traffic-summary-used-sub">{{ usedSub }}</span>
      </div>
      <div class="traffic-summary-card__metric traffic-summary-card__metric--primary">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 2v4"/>
            <path d="M12 18v4"/>
            <path d="M4.93 4.93l2.83 2.83"/>
            <path d="M16.24 16.24l2.83 2.83"/>
            <path d="M2 12h4"/>
            <path d="M18 12h4"/>
            <path d="M4.93 19.07l2.83-2.83"/>
            <path d="M16.24 7.76l2.83-2.83"/>
          </svg>
          <span class="traffic-summary-card__label">剩余</span>
        </div>
        <span class="traffic-summary-card__value" data-testid="traffic-summary-remaining">{{ remainingDisplay }}</span>
        <span v-if="remainingSub" class="traffic-summary-card__sub" data-testid="traffic-summary-remaining-sub">{{ remainingSub }}</span>
      </div>
      <div class="traffic-summary-card__metric traffic-summary-card__metric--secondary">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="19" x2="12" y2="5"/>
            <polyline points="5 12 12 5 19 12"/>
          </svg>
          <span class="traffic-summary-card__label">上行</span>
        </div>
        <span class="traffic-summary-card__value">{{ formatBytes(summary.tx_bytes) }}</span>
      </div>
      <div class="traffic-summary-card__metric traffic-summary-card__metric--secondary">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="5" x2="12" y2="19"/>
            <polyline points="19 12 12 19 5 12"/>
          </svg>
          <span class="traffic-summary-card__label">下行</span>
        </div>
        <span class="traffic-summary-card__value">{{ formatBytes(summary.rx_bytes) }}</span>
      </div>
      <div class="traffic-summary-card__metric traffic-summary-card__metric--secondary">
        <div class="traffic-summary-card__header">
          <svg class="traffic-summary-card__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
          </svg>
          <span class="traffic-summary-card__label">当前速率</span>
        </div>
        <span v-if="rateRows.length" class="traffic-summary-card__value traffic-summary-card__value--rates">
          <span
            v-for="row in rateRows"
            :key="row.key"
            class="traffic-summary-card__rate-row"
            data-testid="traffic-summary-card-rate-row"
          >
            <span class="traffic-summary-card__rate-arrow">{{ row.arrow }}</span>
            <span class="traffic-summary-card__rate-value">{{ row.value }}</span>
          </span>
        </span>
        <span v-else class="traffic-summary-card__value">—</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, formatQuota, usagePercent } from '../../utils/trafficStats.js'
import { rate } from '../../utils/agentMetrics.js'

const props = defineProps({
  summary: { type: Object, default: () => ({}) },
  direction: { type: String, default: 'both' },
  networkMetrics: { type: Object, default: null },
  /** When true, show loading placeholder and never treat empty summary as unlimited. */
  loading: { type: Boolean, default: false }
})

const showLoading = computed(() => props.loading === true)

const percent = computed(() => usagePercent(props.summary.used_bytes, props.summary.monthly_quota_bytes))

const hasQuota = computed(() => props.summary.monthly_quota_bytes != null && props.summary.monthly_quota_bytes !== '')

const usedSub = computed(() => {
  if (showLoading.value) return null
  if (!hasQuota.value) return null
  if (percent.value == null) return null
  return `占额度 ${percent.value}% · 额度 ${formatQuota(props.summary.monthly_quota_bytes, '无限制')}`
})

const remainingDisplay = computed(() => {
  if (showLoading.value) return '—'
  if (!hasQuota.value) return '无限制'
  if (props.summary.remaining_bytes != null && props.summary.remaining_bytes !== '') {
    return formatBytes(props.summary.remaining_bytes)
  }
  const used = Number(props.summary.used_bytes) || 0
  const quota = Number(props.summary.monthly_quota_bytes) || 0
  return formatBytes(Math.max(0, quota - used))
})

const remainingSub = computed(() => {
  if (showLoading.value) return null
  if (!hasQuota.value) return '未设置月额度'
  return null
})

const rateRows = computed(() => {
  if (showLoading.value) return []
  const rx = props.networkMetrics?.rx_bytes_per_second
  const tx = props.networkMetrics?.tx_bytes_per_second
  const rows = []
  if (rx != null && rx !== '') {
    rows.push({ key: 'down', arrow: '↓', value: rate(rx) })
  }
  if (tx != null && tx !== '') {
    rows.push({ key: 'up', arrow: '↑', value: rate(tx) })
  }
  return rows
})
</script>

<style scoped>
.traffic-summary-cards {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 0.875rem 1rem;
}
.traffic-summary-cards__loading {
  min-height: 4.5rem;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-tertiary);
  font-size: 0.875rem;
  font-weight: 500;
}
.traffic-summary-cards--loading {
  opacity: 0.92;
}
.traffic-summary-cards__grid {
  display: grid;
  /* total/remaining equal primary; uplink/downlink/rate equal secondary block */
  grid-template-columns: minmax(0, 1.1fr) minmax(0, 1.1fr) minmax(0, 0.95fr) minmax(0, 0.95fr) minmax(0, 0.95fr);
  gap: 0.375rem 0.625rem;
  align-items: stretch;
}
.traffic-summary-card__metric {
  min-width: 0;
  text-align: left;
  padding: 0.375rem 0.5rem;
  border-radius: var(--radius-md);
  transition: background-color 0.15s ease, border-color 0.15s ease;
}
.traffic-summary-card__metric:hover {
  background: var(--color-bg-subtle);
}
.traffic-summary-card__metric--primary {
  padding: 0.5rem 0.625rem;
}
.traffic-summary-card__metric--secondary {
  padding: 0.25rem 0.375rem;
  opacity: 0.88;
}
.traffic-summary-card__metric--secondary:hover {
  opacity: 1;
  background: transparent;
}
.traffic-summary-card__header {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  margin-bottom: 0.2rem;
}
.traffic-summary-card__icon {
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}
.traffic-summary-card__metric--primary .traffic-summary-card__icon {
  color: var(--color-primary);
}
.traffic-summary-card__metric--secondary .traffic-summary-card__icon {
  color: var(--color-text-muted);
  width: 13px;
  height: 13px;
  opacity: 0.85;
}
.traffic-summary-card__label {
  display: block;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 500;
  letter-spacing: 0.01em;
}
.traffic-summary-card__metric--secondary .traffic-summary-card__label {
  color: var(--color-text-muted);
  font-weight: 400;
  font-size: 0.6875rem;
}
.traffic-summary-card__value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.25;
}
.traffic-summary-card__metric--primary .traffic-summary-card__value {
  font-size: 1.1875rem;
  color: var(--color-text-primary);
}
.traffic-summary-card__metric--secondary .traffic-summary-card__value {
  font-size: 0.8125rem;
  font-weight: 500;
  color: var(--color-text-tertiary);
}
.traffic-summary-card__sub {
  display: block;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  margin-top: 0.2rem;
  line-height: 1.35;
}
.traffic-summary-card__value--rates {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
  font-size: 0.8125rem;
}
.traffic-summary-card__rate-row {
  display: flex;
  align-items: baseline;
  gap: 0.25rem;
  min-width: 0;
}
.traffic-summary-card__rate-arrow {
  font-weight: var(--font-bold);
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}
.traffic-summary-card__rate-value {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-variant-numeric: tabular-nums;
}
@media (max-width: 1100px) {
  .traffic-summary-cards__grid {
    grid-template-columns: minmax(0, 1.1fr) minmax(0, 1.1fr) minmax(0, 1fr);
  }
}
@media (max-width: 720px) {
  .traffic-summary-cards__grid {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }
}
@media (max-width: 480px) {
  .traffic-summary-cards__grid {
    grid-template-columns: 1fr;
  }
}
</style>
