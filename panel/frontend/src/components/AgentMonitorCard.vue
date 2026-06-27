<template>
  <BaseListCard
    class="agent-monitor-card"
    :status="statusTone"
    :title="displayName"
    @click="$emit('details', agent)"
  >
    <template #header-left>
      <AgentStatusBadge :agent="agent" class="agent-monitor-card__status" />
      <span class="agent-monitor-card__name" data-testid="monitor-card-name">{{ displayName }}</span>
    </template>
    <template #header-right>
      <BaseIconButton
        title="查看详情"
        tone="primary"
        class="agent-monitor-card__detail-btn"
        @click="$emit('details', agent)"
      >
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7-11-7-11-7z"/>
          <circle cx="12" cy="12" r="3"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="agent-monitor-card__meta">
      <div class="agent-monitor-card__meta-item">
        <span class="agent-monitor-card__meta-label">地址</span>
        <span data-testid="monitor-card-endpoint">{{ endpointLabel }}</span>
      </div>
      <div class="agent-monitor-card__meta-item">
        <span class="agent-monitor-card__meta-label">最后活跃</span>
        <span data-testid="monitor-card-last-seen">{{ timeAgo(agent.last_seen_at) }}</span>
      </div>
    </div>

    <div class="agent-monitor-card__grid">
      <div class="agent-monitor-card__cell" data-testid="monitor-card-cpu">
        <div class="agent-monitor-card__cell-header">
          <i class="i-mdi-cpu agent-monitor-card__icon" />
          <span class="agent-monitor-card__label">CPU</span>
        </div>
        <div class="agent-monitor-card__value" data-testid="monitor-card-cpu-value">{{ cpuUsage(metrics) }}</div>
        <div class="agent-monitor-card__subvalue" data-testid="monitor-card-cpu-percent">{{ percent(metrics.cpu_usage_percent) }}</div>
        <div class="agent-monitor-card__bar-bg">
          <div
            class="agent-monitor-card__bar"
            :class="barClass(metrics.cpu_usage_percent)"
            :style="{ width: `${clamp(metrics.cpu_usage_percent)}%` }"
          />
        </div>
      </div>

      <div class="agent-monitor-card__cell" data-testid="monitor-card-memory">
        <div class="agent-monitor-card__cell-header">
          <i class="i-mdi-memory agent-monitor-card__icon" />
          <span class="agent-monitor-card__label">内存</span>
        </div>
        <div class="agent-monitor-card__value" data-testid="monitor-card-memory-value">{{ bytesPair(metrics.memory_used_bytes, metrics.memory_total_bytes) }}</div>
        <div class="agent-monitor-card__subvalue" data-testid="monitor-card-memory-percent">{{ percent(metrics.memory_usage_percent) }}</div>
        <div class="agent-monitor-card__bar-bg">
          <div
            class="agent-monitor-card__bar"
            :class="barClass(metrics.memory_usage_percent)"
            :style="{ width: `${clamp(metrics.memory_usage_percent)}%` }"
          />
        </div>
      </div>

      <div class="agent-monitor-card__cell" data-testid="monitor-card-disk">
        <div class="agent-monitor-card__cell-header">
          <i class="i-mdi-harddisk agent-monitor-card__icon" />
          <span class="agent-monitor-card__label">磁盘</span>
        </div>
        <div class="agent-monitor-card__value" data-testid="monitor-card-disk-value">{{ bytesPair(metrics.disk_used_bytes, metrics.disk_total_bytes) }}</div>
        <div class="agent-monitor-card__subvalue" data-testid="monitor-card-disk-percent">{{ percent(metrics.disk_usage_percent) }}</div>
        <div class="agent-monitor-card__bar-bg">
          <div
            class="agent-monitor-card__bar"
            :class="barClass(metrics.disk_usage_percent)"
            :style="{ width: `${clamp(metrics.disk_usage_percent)}%` }"
          />
        </div>
      </div>

      <div class="agent-monitor-card__cell" data-testid="monitor-card-network">
        <div class="agent-monitor-card__cell-header">
          <i class="i-mdi-network agent-monitor-card__icon" />
          <span class="agent-monitor-card__label">网络</span>
        </div>
        <div class="agent-monitor-card__value" data-testid="monitor-card-network-down">↓ {{ rate(network?.rx_bytes_per_second) }}</div>
        <div class="agent-monitor-card__subvalue" data-testid="monitor-card-network-up">↑ {{ rate(network?.tx_bytes_per_second) }}</div>
      </div>
    </div>

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in agent.tags" :key="tag" tone="neutral" class="agent-monitor-card__tag">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import AgentStatusBadge from './AgentStatusBadge.vue'
import BaseBadge from './base/BaseBadge.vue'
import BaseIconButton from './base/BaseIconButton.vue'
import BaseListCard from './base/BaseListCard.vue'
import { getAgentStatus, getHostname, timeAgo } from '../utils/agentHelpers.js'
import { barTone, bytesPair, clamp, cpuUsage, percent, rate } from '../utils/agentMetrics.js'

const props = defineProps({
  agent: { type: Object, required: true }
})

defineEmits(['details'])

const STATUS_TONE = {
  online: 'success',
  offline: 'neutral',
  failed: 'danger',
  pending: 'warning',
}

const BAR_TONE_CLASS = {
  success: 'agent-monitor-card__bar--success',
  warning: 'agent-monitor-card__bar--warning',
  danger: 'agent-monitor-card__bar--danger',
  neutral: 'agent-monitor-card__bar--neutral',
}

const displayName = computed(() => props.agent.name || props.agent.id || '未命名节点')
const statusTone = computed(() => STATUS_TONE[getAgentStatus(props.agent)] || 'neutral')
const endpointLabel = computed(() => props.agent.agent_url ? getHostname(props.agent.agent_url) : (props.agent.last_seen_ip || '—'))
const metrics = computed(() => props.agent.monitor?.metrics || props.agent.metrics || {})
const network = computed(() => metrics.value.network || null)
const hasTags = computed(() => Array.isArray(props.agent.tags) && props.agent.tags.length > 0)

function barClass(value) {
  return BAR_TONE_CLASS[barTone(value)] || BAR_TONE_CLASS.neutral
}
</script>

<style scoped>
.agent-monitor-card {
  --amc-green: var(--color-primary, #059669);
  --amc-green-subtle: color-mix(in srgb, var(--amc-green) 8%, transparent);
  --amc-green-border: color-mix(in srgb, var(--amc-green) 15%, transparent);
  --amc-status-success: var(--color-success, #059669);
  --amc-status-warning: var(--color-warning, #d97706);
  --amc-status-danger: var(--color-danger, #dc2626);
  --amc-status-neutral: var(--color-text-muted, #9ca3af);
}

:deep(.base-list-card) {
  position: relative;
  overflow: hidden;
}

:deep(.base-list-card)::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 4px;
  background: var(--amc-status-neutral);
  transition: background 150ms ease;
}

:deep(.base-list-card[data-status="success"])::before {
  background: var(--amc-status-success);
}

:deep(.base-list-card[data-status="warning"])::before {
  background: var(--amc-status-warning);
}

:deep(.base-list-card[data-status="danger"])::before {
  background: var(--amc-status-danger);
}

:deep(.base-list-card[data-status="neutral"])::before {
  background: var(--amc-status-neutral);
}

:deep(.base-list-card__header) {
  align-items: flex-start;
}

:deep(.base-list-card__header-left) {
  gap: 0.625rem;
}

.agent-monitor-card__status {
  flex-shrink: 0;
}

.agent-monitor-card__name {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text-primary);
  line-height: 1.35;
  word-break: break-all;
}

.agent-monitor-card__meta {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-family: var(--font-mono);
  margin-top: -0.125rem;
}

.agent-monitor-card__meta-item {
  display: flex;
  align-items: baseline;
  gap: 0.375rem;
}

.agent-monitor-card__meta-label {
  font-size: 0.625rem;
  color: var(--amc-status-neutral);
  flex-shrink: 0;
}

.agent-monitor-card__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.5rem;
  margin-top: 0.25rem;
}

.agent-monitor-card__cell {
  min-width: 0;
  padding: 0.625rem 0.75rem;
  border: 1px solid var(--amc-green-border);
  border-radius: var(--radius-md);
  background: var(--amc-green-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
  transition: background 150ms ease, border-color 150ms ease;
}

.agent-monitor-card__cell-header {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  padding-bottom: 0.25rem;
  border-bottom: 1px solid rgba(255, 255, 255, 0.12);
  margin-bottom: 0.125rem;
}

.agent-monitor-card__icon {
  width: 0.875rem;
  height: 0.875rem;
  color: var(--amc-green);
  flex-shrink: 0;
}

.agent-monitor-card__label {
  color: var(--color-text-tertiary);
  font-size: 0.7rem;
  line-height: 1;
}

.agent-monitor-card__value {
  color: var(--color-text-primary);
  font-size: 0.8rem;
  font-weight: 600;
  line-height: 1.3;
  overflow-wrap: anywhere;
}

.agent-monitor-card__subvalue {
  color: var(--color-text-secondary);
  font-size: 0.7rem;
  line-height: 1.2;
}

.agent-monitor-card__bar-bg {
  height: 4px;
  background: var(--color-bg-subtle);
  border-radius: 999px;
  overflow: hidden;
  margin-top: 0.25rem;
}

.agent-monitor-card__bar {
  height: 100%;
  border-radius: 999px;
  transition: width 300ms ease, background 150ms ease;
}

.agent-monitor-card__bar--success {
  background: var(--amc-status-success);
}

.agent-monitor-card__bar--warning {
  background: var(--amc-status-warning);
}

.agent-monitor-card__bar--danger {
  background: var(--amc-status-danger);
}

.agent-monitor-card__bar--neutral {
  background: var(--amc-status-neutral);
}

:deep(.base-list-card__footer) {
  margin-top: -0.125rem;
}

.agent-monitor-card__tag {
  font-size: 0.7rem;
}

@media (max-width: 420px) {
  .agent-monitor-card__grid {
    grid-template-columns: 1fr;
  }
}
</style>
