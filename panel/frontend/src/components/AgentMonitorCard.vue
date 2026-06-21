<template>
  <BaseListCard :status="statusTone" :title="displayName" @click="$emit('details', agent)">
    <template #header-left>
      <AgentStatusBadge :agent="agent" />
      <BaseBadge tone="primary">{{ modeLabel }}</BaseBadge>
    </template>
    <template #header-right>
      <BaseIconButton title="查看详情" tone="primary" @click="$emit('details', agent)">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7-11-7-11-7z"/>
          <circle cx="12" cy="12" r="3"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="agent-monitor-card__meta">
      <span>{{ endpointLabel }}</span>
      <span>{{ timeAgo(agent.last_seen_at) }}</span>
    </div>

    <div class="agent-monitor-card__metrics">
      <div class="agent-monitor-card__metric">
        <span class="agent-monitor-card__label">CPU</span>
        <strong>{{ cpuUsage(metrics) }}</strong>
        <small>{{ percent(metrics.cpu_usage_percent) }}</small>
      </div>
      <div class="agent-monitor-card__metric">
        <span class="agent-monitor-card__label">内存</span>
        <strong>{{ bytesPair(metrics.memory_used_bytes, metrics.memory_total_bytes) }}</strong>
        <small>{{ percent(metrics.memory_usage_percent) }}</small>
      </div>
      <div class="agent-monitor-card__metric">
        <span class="agent-monitor-card__label">磁盘</span>
        <strong>{{ bytesPair(metrics.disk_used_bytes, metrics.disk_total_bytes) }}</strong>
        <small>{{ percent(metrics.disk_usage_percent) }}</small>
      </div>
    </div>

    <div class="agent-monitor-card__network">
      <div>
        <span class="agent-monitor-card__label">累计下行</span>
        <strong>{{ bytes(network?.rx_bytes) }}</strong>
      </div>
      <div>
        <span class="agent-monitor-card__label">累计上行</span>
        <strong>{{ bytes(network?.tx_bytes) }}</strong>
      </div>
      <div>
        <span class="agent-monitor-card__label">下行速率</span>
        <strong>{{ rate(network?.rx_bytes_per_second) }}</strong>
      </div>
      <div>
        <span class="agent-monitor-card__label">上行速率</span>
        <strong>{{ rate(network?.tx_bytes_per_second) }}</strong>
      </div>
    </div>

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in agent.tags" :key="tag" tone="neutral">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import AgentStatusBadge from './AgentStatusBadge.vue'
import BaseBadge from './base/BaseBadge.vue'
import BaseIconButton from './base/BaseIconButton.vue'
import BaseListCard from './base/BaseListCard.vue'
import { getAgentStatus, getHostname, getModeLabel, timeAgo } from '../utils/agentHelpers.js'

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

const displayName = computed(() => props.agent.name || props.agent.id || '未命名节点')
const statusTone = computed(() => STATUS_TONE[getAgentStatus(props.agent)] || 'neutral')
const modeLabel = computed(() => getModeLabel(props.agent.mode))
const endpointLabel = computed(() => props.agent.agent_url ? getHostname(props.agent.agent_url) : (props.agent.last_seen_ip || '—'))
const metrics = computed(() => props.agent.monitor?.metrics || props.agent.metrics || {})
const network = computed(() => metrics.value.network || null)
const hasTags = computed(() => Array.isArray(props.agent.tags) && props.agent.tags.length > 0)

function percent(value) {
  if (value === null || value === undefined || value === '') return '—'
  return Number.isFinite(Number(value)) ? `${Number(value).toFixed(1)}%` : '—'
}

function bytes(value) {
  if (value === null || value === undefined || value === '') return '—'
  const n = Number(value)
  if (!Number.isFinite(n)) return '—'
  if (n >= 1024 ** 4) return `${(n / 1024 ** 4).toFixed(1)} TB`
  if (n >= 1024 ** 3) return `${(n / 1024 ** 3).toFixed(1)} GB`
  if (n >= 1024 ** 2) return `${(n / 1024 ** 2).toFixed(1)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${Math.max(0, Math.round(n))} B`
}

function rate(value) {
  if (value === null || value === undefined || value === '') return '—'
  const n = Number(value)
  if (!Number.isFinite(n)) return '—'
  return `${bytes(n)}/s`
}

function cpuUsage(source = {}) {
  const used = Number(source.cpu_used_cores)
  const total = Number(source.cpu_total_cores)
  if (Number.isFinite(used) && Number.isFinite(total) && total > 0) {
    return `${used.toFixed(1)} / ${total.toFixed(0)} 核`
  }
  if (Number.isFinite(used)) return `${used.toFixed(1)} 核`
  return percent(source.cpu_usage_percent)
}

function bytesPair(usedValue, totalValue) {
  const used = Number(usedValue)
  const total = Number(totalValue)
  if (Number.isFinite(used) && Number.isFinite(total) && total > 0) {
    return `${bytes(used)} / ${bytes(total)}`
  }
  if (Number.isFinite(used)) return bytes(used)
  return '—'
}
</script>

<style scoped>
.agent-monitor-card__meta {
  display: flex;
  justify-content: space-between;
  gap: 0.75rem;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-family: var(--font-mono);
}

.agent-monitor-card__metrics,
.agent-monitor-card__network {
  display: grid;
  gap: 0.5rem;
}

.agent-monitor-card__metrics {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.agent-monitor-card__network {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.agent-monitor-card__metric,
.agent-monitor-card__network > div {
  min-width: 0;
  padding: 0.5rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
}

.agent-monitor-card__label {
  display: block;
  margin-bottom: 0.25rem;
  color: var(--color-text-tertiary);
  font-size: 0.7rem;
}

.agent-monitor-card__metric strong,
.agent-monitor-card__network strong {
  display: block;
  color: var(--color-text-primary);
  font-size: 0.875rem;
  line-height: 1.2;
  overflow-wrap: anywhere;
}

.agent-monitor-card__metric small {
  display: block;
  margin-top: 0.125rem;
  color: var(--color-text-tertiary);
  font-size: 0.7rem;
  line-height: 1.2;
}

@media (max-width: 420px) {
  .agent-monitor-card__metrics,
  .agent-monitor-card__network {
    grid-template-columns: 1fr;
  }
}
</style>
