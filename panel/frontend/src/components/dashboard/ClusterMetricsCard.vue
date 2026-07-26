<template>
  <section class="cluster-metrics">
    <h2 class="cluster-metrics__title">集群指标</h2>

    <RouterLink to="/agents" class="cluster-metrics__nodes" data-testid="metric-nodes">
      <svg class="cluster-metrics__ring" data-testid="metric-ring" viewBox="0 0 96 96" width="84" height="84">
        <circle class="cluster-metrics__ring-track" cx="48" cy="48" r="40" />
        <circle
          class="cluster-metrics__ring-value"
          :class="ringToneClass"
          cx="48" cy="48" r="40"
          :stroke-dasharray="ringCircumference"
          :stroke-dashoffset="ringOffset"
        />
      </svg>
      <span class="cluster-metrics__nodes-text">
        <span class="cluster-metrics__nodes-value">
          <strong>{{ onlineCount }}</strong><span class="cluster-metrics__nodes-total">/ {{ agents.length }}</span>
        </span>
        <span class="cluster-metrics__nodes-label">节点在线</span>
        <span v-if="offlineCount > 0" class="cluster-metrics__nodes-sub cluster-metrics__nodes-sub--danger">{{ offlineCount }} 个离线</span>
        <span v-else class="cluster-metrics__nodes-sub">全部在线</span>
      </span>
    </RouterLink>

    <div class="cluster-metrics__rows">
      <RouterLink to="/rules" class="cluster-metrics__row" data-testid="metric-http">
        <span class="cluster-metrics__row-head">
          <span class="cluster-metrics__row-label">HTTP 规则</span>
          <span class="cluster-metrics__row-value">{{ httpTotal }}</span>
        </span>
        <span class="cluster-metrics__bar">
          <span
            v-for="seg in httpSegments"
            :key="seg.id"
            class="cluster-metrics__bar-seg"
            :style="{ width: seg.percent + '%', background: seg.color }"
            :title="`${seg.name}: ${seg.count}`"
          />
        </span>
      </RouterLink>

      <RouterLink to="/l4" class="cluster-metrics__row" data-testid="metric-l4">
        <span class="cluster-metrics__row-head">
          <span class="cluster-metrics__row-label">L4 规则</span>
          <span class="cluster-metrics__row-value">{{ l4Total }}</span>
        </span>
        <span class="cluster-metrics__bar">
          <span
            v-for="seg in l4Segments"
            :key="seg.id"
            class="cluster-metrics__bar-seg"
            :style="{ width: seg.percent + '%', background: seg.color }"
            :title="`${seg.name}: ${seg.count}`"
          />
        </span>
      </RouterLink>

      <RouterLink
        :to="certsLink"
        class="cluster-metrics__row"
        :class="{ 'cluster-metrics__row--danger': certsExpiring > 0 }"
        data-testid="metric-certs"
      >
        <span class="cluster-metrics__row-head">
          <span class="cluster-metrics__row-label">证书</span>
          <span class="cluster-metrics__row-value">{{ certsTotal }}</span>
        </span>
        <span class="cluster-metrics__row-sub">
          {{ certsExpiring > 0 ? `${certsExpiring} 个即将过期` : '证书正常' }}
        </span>
      </RouterLink>
    </div>
  </section>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  agents: { type: Array, default: () => [] },
  certsTotal: { type: Number, default: 0 },
  certsExpiring: { type: Number, default: 0 },
  defaultAgentId: { type: String, default: '' }
})

const DISTRIBUTION_COLORS = ['#60a5fa', '#a78bfa', '#34d399', '#fbbf24', '#f87171', '#22d3ee', '#f472b6']

const onlineCount = computed(() => props.agents.filter(a => a.status === 'online').length)
const offlineCount = computed(() => props.agents.length - onlineCount.value)

const ringCircumference = 2 * Math.PI * 40
const ringOffset = computed(() => {
  const total = props.agents.length
  const ratio = total > 0 ? onlineCount.value / total : 0
  return ringCircumference * (1 - ratio)
})
// 环弧画的是在线占比,颜色保持 success,全部离线才转 danger,避免「红弧=在线」的误导
const ringToneClass = computed(() => {
  if (!props.agents.length) return 'cluster-metrics__ring-value--muted'
  if (onlineCount.value === 0) return 'cluster-metrics__ring-value--danger'
  return 'cluster-metrics__ring-value--success'
})

function segmentsBy(countKey) {
  const contributors = props.agents
    .map((agent, i) => ({
      id: agent.id || String(i),
      name: agent.name || agent.id,
      count: agent[countKey] || 0
    }))
    .filter(item => item.count > 0)
    .sort((a, b) => b.count - a.count)
  const total = contributors.reduce((sum, item) => sum + item.count, 0)
  if (!total) return []
  // 只展示前 5 个节点,其余合并为「其他」,避免多节点时分布条碎成彩虹
  const top = contributors.slice(0, 5)
  const restCount = contributors.slice(5).reduce((sum, item) => sum + item.count, 0)
  const segments = top.map((item, i) => ({
    ...item,
    percent: Math.max(2, Math.round((item.count / total) * 100)),
    color: DISTRIBUTION_COLORS[i % DISTRIBUTION_COLORS.length]
  }))
  if (restCount > 0) {
    segments.push({
      id: '__rest',
      name: '其他',
      count: restCount,
      percent: Math.max(2, Math.round((restCount / total) * 100)),
      color: 'var(--color-border-strong, #cbd5e1)'
    })
  }
  return segments
}

const httpTotal = computed(() => props.agents.reduce((sum, a) => sum + (a.http_rules_count || 0), 0))
const l4Total = computed(() => props.agents.reduce((sum, a) => sum + (a.l4_rules_count || 0), 0))
const httpSegments = computed(() => segmentsBy('http_rules_count'))
const l4Segments = computed(() => segmentsBy('l4_rules_count'))

const certsLink = computed(() => props.defaultAgentId ? `/certs?agentId=${props.defaultAgentId}` : '/certs')
</script>

<style scoped>
.cluster-metrics {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.cluster-metrics__title {
  font-size: var(--text-base);
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0 0 var(--space-3);
}

.cluster-metrics__nodes {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-2);
  margin: 0 calc(-1 * var(--space-2));
  border-radius: var(--radius-xl);
  text-decoration: none;
  transition: background var(--duration-fast) var(--ease-default);
}

.cluster-metrics__nodes:hover {
  background: var(--color-bg-hover);
}

.cluster-metrics__ring {
  flex-shrink: 0;
  transform: rotate(-90deg);
}

.cluster-metrics__ring-track {
  fill: none;
  stroke: var(--color-border-subtle);
  stroke-width: 9;
}

.cluster-metrics__ring-value {
  fill: none;
  stroke-width: 9;
  stroke-linecap: round;
  transition: stroke-dashoffset 0.65s var(--ease-default, ease);
  animation: cluster-ring-enter 0.7s var(--ease-default, ease) both;
}

@keyframes cluster-ring-enter {
  from { opacity: 0; }
  to { opacity: 1; }
}

.cluster-metrics__ring-value--success { stroke: var(--color-success, #34d399); }
.cluster-metrics__ring-value--danger { stroke: var(--color-danger, #ef4444); }
.cluster-metrics__ring-value--muted { stroke: var(--color-text-muted); }

.cluster-metrics__nodes-text {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  min-width: 0;
}

.cluster-metrics__nodes-value {
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary);
}

.cluster-metrics__nodes-value strong {
  font-size: var(--text-2xl);
  font-weight: 700;
}

.cluster-metrics__nodes-total {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin-left: 0.25rem;
}

.cluster-metrics__nodes-label {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}

.cluster-metrics__nodes-sub {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.cluster-metrics__nodes-sub--danger {
  color: var(--color-danger, #ef4444);
  font-weight: 600;
}

.cluster-metrics__rows {
  display: flex;
  flex-direction: column;
  margin-top: var(--space-2);
}

.cluster-metrics__row {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  padding: var(--space-3) var(--space-2);
  margin: 0 calc(-1 * var(--space-2));
  border-radius: var(--radius-lg);
  border-top: 1px solid var(--color-border-subtle);
  text-decoration: none;
  transition: background var(--duration-fast) var(--ease-default);
}

.cluster-metrics__row:hover {
  background: var(--color-bg-hover);
}

.cluster-metrics__row-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

.cluster-metrics__row-label {
  font-size: var(--text-xs);
  font-weight: 500;
  color: var(--color-text-tertiary);
}

.cluster-metrics__row-value {
  font-size: var(--text-lg);
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary);
}

.cluster-metrics__row--danger .cluster-metrics__row-value {
  color: var(--color-danger, #ef4444);
}

.cluster-metrics__bar {
  display: flex;
  gap: 2px;
  height: 6px;
  border-radius: var(--radius-full);
  overflow: hidden;
  background: var(--color-bg-subtle);
}

.cluster-metrics__bar-seg {
  height: 100%;
  border-radius: var(--radius-full);
  min-width: 4px;
  animation: cluster-bar-enter 0.55s var(--ease-default, ease) both;
  transform-origin: left center;
}

@keyframes cluster-bar-enter {
  from { transform: scaleX(0); opacity: 0.4; }
  to { transform: scaleX(1); opacity: 1; }
}

.cluster-metrics__row-sub {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.cluster-metrics__row--danger .cluster-metrics__row-sub {
  color: var(--color-danger, #ef4444);
  font-weight: 600;
}
</style>
