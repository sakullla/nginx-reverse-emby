<template>
  <BaseListCard
    class="agent-monitor-card"
    :status="statusTone"
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

    <div class="agent-monitor-card__metrics">
      <AgentMetricTile
        data-testid="monitor-card-cpu"
        icon="i-mdi-cpu-64-bit"
        label="CPU"
        variant="compact"
        display-mode="ring"
        :value="cpuUsage(metrics, { compact: true })"
        :percent="metrics.cpu_usage_percent"
        :tone="barTone(metrics.cpu_usage_percent)"
      />
      <AgentMetricTile
        data-testid="monitor-card-memory"
        icon="i-mdi-memory"
        label="内存"
        variant="compact"
        display-mode="ring"
        :value="bytesPair(metrics.memory_used_bytes, metrics.memory_total_bytes, { compact: true })"
        :percent="metrics.memory_usage_percent"
        :tone="barTone(metrics.memory_usage_percent)"
      />
      <AgentMetricTile
        data-testid="monitor-card-disk"
        icon="i-mdi-harddisk"
        label="磁盘"
        variant="compact"
        display-mode="ring"
        :value="bytesPair(metrics.disk_used_bytes, metrics.disk_total_bytes, { compact: true })"
        :percent="metrics.disk_usage_percent"
        :tone="barTone(metrics.disk_usage_percent)"
      />
      <AgentMetricTile
        data-testid="monitor-card-network"
        icon="i-mdi-network"
        label="网络"
        variant="compact"
        :network-down="rate(network?.rx_bytes_per_second)"
        :network-up="rate(network?.tx_bytes_per_second)"
      />
    </div>

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in agent.tags" :key="tag" tone="neutral" class="agent-monitor-card__tag">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import AgentMetricTile from './AgentMetricTile.vue'
import AgentStatusBadge from './AgentStatusBadge.vue'
import BaseBadge from './base/BaseBadge.vue'
import BaseIconButton from './base/BaseIconButton.vue'
import BaseListCard from './base/BaseListCard.vue'
import { getAgentStatus, getHostname, timeAgo } from '../utils/agentHelpers.js'
import { barTone, bytesPair, cpuUsage, rate } from '../utils/agentMetrics.js'

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
const endpointLabel = computed(() => props.agent.agent_url ? getHostname(props.agent.agent_url) : (props.agent.ddns_domain || props.agent.last_seen_ip || '—'))
const metrics = computed(() => props.agent.monitor?.metrics || props.agent.metrics || {})
const network = computed(() => metrics.value.network || null)
const hasTags = computed(() => Array.isArray(props.agent.tags) && props.agent.tags.length > 0)
</script>

<style scoped>
/* Status strip lives on BaseListCard via data-status; do not re-draw here. */

.agent-monitor-card__status {
  flex-shrink: 0;
}

.agent-monitor-card__name {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.35;
  word-break: break-all;
}

.agent-monitor-card__meta {
  display: flex;
  flex-direction: column;
  gap: var(--space-0-5);
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  font-family: var(--font-mono);
}

.agent-monitor-card__meta-item {
  display: flex;
  align-items: baseline;
  gap: var(--space-1-5);
}

.agent-monitor-card__meta-label {
  font-size: var(--text-2xs);
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.agent-monitor-card__metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-1-5);
  align-items: stretch;
}

.agent-monitor-card__tag {
  font-size: var(--text-xs);
}

@media (max-width: 420px) {
  .agent-monitor-card__metrics {
    grid-template-columns: 1fr;
  }
}
</style>
