<template>
  <div class="agent-tiles" :class="{ 'agent-tiles--detailed': detailed }">
    <RouterLink
      v-for="agent in agents"
      :key="agent.id"
      :to="`/agents/${agent.id}`"
      class="agent-tile"
      :class="tileClass(agent)"
      data-testid="agent-tile"
    >
      <span class="agent-tile__dot" aria-hidden="true"></span>
      <span class="agent-tile__body">
        <span class="agent-tile__name">{{ agent.name || agent.id }}</span>
        <span class="agent-tile__meta">
          <span class="agent-tile__status">{{ statusLabel(agent) }}</span>
          <span v-if="endpointLabel(agent)" class="agent-tile__endpoint">{{ endpointLabel(agent) }}</span>
        </span>
      </span>
      <span v-if="detailed" class="agent-tile__counts">
        <span>HTTP {{ agent.http_rules_count || 0 }}</span>
        <span>L4 {{ agent.l4_rules_count || 0 }}</span>
      </span>
    </RouterLink>
  </div>
</template>

<script setup>
import { getAgentSyncStatus } from '../../utils/syncStatus'
import { getAgentEndpointLabel } from '../../utils/agentHelpers'

defineProps({
  agents: { type: Array, default: () => [] },
  // 少量节点时切整行详细模式,避免宽卡里只有几个小磁贴
  detailed: { type: Boolean, default: false }
})

const STATUS_LABELS = {
  online: '已同步',
  pending: '待同步',
  failed: '同步失败',
  offline: '离线'
}

function statusLabel(agent) {
  return STATUS_LABELS[getAgentSyncStatus(agent)] || '未知'
}

// 磁贴副信息展示节点入口地址:优先 DDNS 域名,其次 agent_url 主机名 / last_seen IP
function endpointLabel(agent) {
  const label = getAgentEndpointLabel(agent)
  return label === '—' ? '' : label
}

function tileClass(agent) {
  const status = getAgentSyncStatus(agent)
  return {
    'agent-tile--offline': status === 'offline',
    'agent-tile--failed': status === 'failed',
    'agent-tile--pending': status === 'pending'
  }
}
</script>

<style scoped>
.agent-tiles {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: var(--space-3);
  align-content: start;
}

.agent-tiles--detailed {
  grid-template-columns: 1fr;
}

.agent-tiles--detailed .agent-tile {
  padding: var(--space-3) var(--space-4);
}

.agent-tile__counts {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  margin-left: auto;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  flex-shrink: 0;
}

.agent-tile {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  text-decoration: none;
  min-width: 0;
  transition: background var(--duration-fast) var(--ease-default),
    border-color var(--duration-fast) var(--ease-default),
    transform var(--duration-fast) var(--ease-default);
}

.agent-tile:hover {
  background: var(--color-bg-hover);
  border-color: var(--color-border-strong);
  transform: translateY(-1px);
}

.agent-tile__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-success, #34d399);
  flex-shrink: 0;
}

.agent-tile--pending .agent-tile__dot {
  background: var(--color-warning, #fbbf24);
}

.agent-tile--failed .agent-tile__dot {
  background: var(--color-danger, #ef4444);
}

.agent-tile--offline .agent-tile__dot {
  background: var(--color-text-muted);
}

.agent-tile--offline {
  border-color: color-mix(in srgb, var(--color-danger, #ef4444) 25%, var(--color-border-subtle));
}

.agent-tile--failed {
  border-color: color-mix(in srgb, var(--color-danger, #ef4444) 35%, var(--color-border-subtle));
}

.agent-tile__body {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-width: 0;
}

.agent-tile__name {
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-tile__meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  min-width: 0;
}

.agent-tile__status {
  flex-shrink: 0;
}

.agent-tile__endpoint {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.agent-tile--offline .agent-tile__status,
.agent-tile--failed .agent-tile__status {
  color: var(--color-danger, #ef4444);
  font-weight: 600;
}

.agent-tile--pending .agent-tile__status {
  color: var(--color-warning, #fbbf24);
}
</style>
