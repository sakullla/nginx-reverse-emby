<template>
  <div v-if="visible" class="dashboard-traffic">
    <div class="dashboard-traffic__header">
      <h2 class="dashboard-traffic__title">流量统计</h2>
      <div class="dashboard-traffic__toolbar">
        <select v-model="selectedAgentId" class="dashboard-traffic__select">
          <option value="">全部节点</option>
          <option v-for="agent in overviewAgents" :key="agent.agent_id" :value="agent.agent_id">{{ agent.name }}</option>
        </select>
      </div>
    </div>
    <div v-if="overviewQuery.isLoading.value" class="dashboard-traffic__loading">
      <div class="spinner"></div>
    </div>
    <template v-else>
      <div class="dashboard-traffic__cards">
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">业务流量</span>
          <span class="dashboard-traffic__card-value">{{ formatBytes(selectedSummary?.used_bytes ?? 0) }}</span>
          <span v-if="selectedPercent != null" class="dashboard-traffic__card-percent" :class="`dashboard-traffic__card-percent--${selectedColor}`">{{ selectedPercent }}%</span>
          <div v-if="selectedPercent != null" class="dashboard-traffic__card-track">
            <div class="dashboard-traffic__card-fill" :class="`dashboard-traffic__card-fill--${selectedColor}`" :style="{ width: `${Math.min(100, selectedPercent)}%` }" />
          </div>
        </div>
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">主机流量 (24h)</span>
          <span class="dashboard-traffic__card-value">{{ formatBytes(hostTotalAccounted) }}</span>
          <span class="dashboard-traffic__card-sub">host_total 口径</span>
        </div>
        <div class="dashboard-traffic__card" :class="{ 'dashboard-traffic__card--alert': blockedCount > 0 }">
          <span class="dashboard-traffic__card-label">阻断节点</span>
          <span class="dashboard-traffic__card-value">{{ blockedCount }} / {{ overviewAgents.length }}</span>
          <span v-if="blockedCount > 0" class="dashboard-traffic__card-sub">{{ blockedCount }} 个节点已超额阻断</span>
        </div>
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">计费周期</span>
          <span class="dashboard-traffic__card-value">{{ cycleLabel }}</span>
          <span class="dashboard-traffic__card-sub">方向: {{ directionLabel }}</span>
        </div>
      </div>
      <div class="dashboard-traffic__chart">
        <TrafficTrendChart :points="trendPoints" :host-points="hostTrendPoints" granularity="day" :quota-bytes="selectedSummary?.quota_bytes ?? null" />
      </div>
      <div class="dashboard-traffic__lists">
        <div class="dashboard-traffic__list-panel">
          <h3 class="dashboard-traffic__list-title">Top 节点</h3>
          <div v-for="agent in topNodes" :key="agent.agent_id" class="dashboard-traffic__list-row">
            <span class="dashboard-traffic__list-name">{{ agent.name || agent.agent_id }}</span>
            <span class="dashboard-traffic__list-value">{{ formatBytes(agent.used_bytes) }}</span>
            <span v-if="agent.quota_bytes != null" class="dashboard-traffic__list-percent">{{ usagePercent(agent.used_bytes, agent.quota_bytes) }}%</span>
          </div>
          <p v-if="!topNodes.length" class="dashboard-traffic__list-empty">暂无节点数据</p>
        </div>
        <div class="dashboard-traffic__list-panel">
          <h3 class="dashboard-traffic__list-title">Top 规则</h3>
          <div v-for="rule in topRules" :key="rule.key" class="dashboard-traffic__list-row">
            <span class="dashboard-traffic__list-name" :title="rule.label">{{ rule.label }}</span>
            <span class="dashboard-traffic__list-value">{{ formatBytes(rule.accounted_bytes) }}</span>
            <span class="dashboard-traffic__list-percent">{{ rule.percent }}%</span>
          </div>
          <p v-if="!topRules.length" class="dashboard-traffic__list-empty">暂无规则数据</p>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useTrafficOverview } from '../../hooks/useTrafficOverview.js'
import { fetchSystemInfo, fetchTrafficSummary } from '../../api'
import TrafficTrendChart from './TrafficTrendChart.vue'
import { formatBytes, usagePercent, quotaColorThreshold } from '../../utils/trafficStats.js'
import { hostTotalForLast24h } from './trafficTrendHelpers.mjs'

const { data: systemInfo } = useQuery({
  queryKey: ['system-info'],
  queryFn: fetchSystemInfo
})

const trafficStatsEnabled = computed(() => !!systemInfo.value && systemInfo.value.traffic_stats_enabled !== false)
const visible = trafficStatsEnabled

const selectedAgentId = ref('')

const overviewQuery = useTrafficOverview(selectedAgentId, trafficStatsEnabled)

const overviewAgents = computed(() => overviewQuery.data.value?.agents ?? [])
const trendPoints = computed(() => normalizePoints(overviewQuery.data.value?.trend ?? []))
const hostTrendPoints = computed(() => normalizePoints(overviewQuery.data.value?.host_trend ?? []))

const selectedSummary = computed(() => {
  const agents = overviewAgents.value
  if (selectedAgentId.value) {
    return agents.find(a => a.agent_id === selectedAgentId.value) ?? null
  }
  if (!agents.length) return null
  return {
    used_bytes: agents.reduce((s, a) => s + (a.used_bytes || 0), 0),
    quota_bytes: agents.every(a => a.quota_bytes == null) ? null : agents.reduce((s, a) => s + (a.quota_bytes || 0), 0),
    remaining_bytes: agents.every(a => a.remaining_bytes == null) ? null : agents.reduce((s, a) => s + (a.remaining_bytes || 0), 0)
  }
})

const selectedPercent = computed(() => usagePercent(selectedSummary.value?.used_bytes, selectedSummary.value?.quota_bytes))
const selectedColor = computed(() => quotaColorThreshold(selectedPercent.value))

const hostTotalAccounted = computed(() => {
  return hostTotalForLast24h(hostTrendPoints.value)
})

const blockedCount = computed(() => overviewAgents.value.filter(a => a.blocked).length)

const cycleLabel = computed(() => {
  const agents = overviewAgents.value
  if (!agents.length) return '—'
  const dirs = new Set(agents.map(a => a.direction || 'both'))
  return dirs.size === 1 ? (agents[0].cycle_start || '—') : '多节点混合'
})

const directionLabel = computed(() => {
  const agents = overviewAgents.value
  if (!agents.length) return '双向'
  const dirs = new Set(agents.map(a => a.direction || 'both'))
  if (dirs.size === 1) {
    const d = Array.from(dirs)[0]
    switch (d) {
      case 'rx': return '入站'
      case 'tx': return '出站'
      case 'max': return '取最大值'
      default: return '双向'
    }
  }
  return '混合'
})

const topNodes = computed(() => {
  const agents = [...overviewAgents.value]
  agents.sort((a, b) => {
    const pa = a.quota_bytes ? a.used_bytes / a.quota_bytes : a.used_bytes
    const pb = b.quota_bytes ? b.used_bytes / b.quota_bytes : b.used_bytes
    return pb - pa
  })
  return agents.slice(0, 8)
})

const agentIdsForTopRules = computed(() => {
  if (selectedAgentId.value) return [selectedAgentId.value]
  return overviewAgents.value.map(a => a.agent_id).filter(Boolean).sort()
})

const topRulesQuery = useQuery({
  queryKey: computed(() => ['traffic-top-rules', selectedAgentId.value || 'all', agentIdsForTopRules.value.join(',')]),
  queryFn: async () => {
    const ids = agentIdsForTopRules.value
    if (!ids.length) return []
    const summaries = await Promise.all(
      ids.map(id => fetchTrafficSummary(id).catch(() => null))
    )
    const ruleMap = new Map()
    for (const summary of summaries) {
      if (!summary) continue
      for (const list of [summary.http_rules, summary.l4_rules, summary.relay_listeners]) {
        if (!Array.isArray(list)) continue
        for (const row of list) {
          const key = `${row.scope_type}-${row.scope_id}`
          const existing = ruleMap.get(key)
          if (existing) {
            existing.accounted_bytes += row.accounted_bytes || 0
            existing.rx_bytes += row.rx_bytes || 0
            existing.tx_bytes += row.tx_bytes || 0
          } else {
            const label = scopeLabel(row.scope_type, row.scope_id)
            ruleMap.set(key, { key, label, accounted_bytes: row.accounted_bytes || 0, rx_bytes: row.rx_bytes || 0, tx_bytes: row.tx_bytes || 0 })
          }
        }
      }
    }
    const rules = Array.from(ruleMap.values())
    const total = rules.reduce((s, r) => s + r.accounted_bytes, 0)
    for (const r of rules) {
      r.percent = total ? Math.round((r.accounted_bytes / total) * 100) : 0
    }
    rules.sort((a, b) => b.accounted_bytes - a.accounted_bytes)
    return rules.slice(0, 10)
  },
  enabled: computed(() => overviewAgents.value.length > 0 && visible.value)
})

const topRules = computed(() => topRulesQuery.data.value ?? [])

function normalizePoints(raw) {
  return (raw || []).map(p => ({
    bucket_start: p.bucket_start,
    rx_bytes: Number(p.rx_bytes) || 0,
    tx_bytes: Number(p.tx_bytes) || 0,
    accounted_bytes: Number(p.accounted_bytes) || 0
  }))
}

function scopeLabel(scopeType, scopeId) {
  switch (scopeType) {
    case 'http_rule': return `HTTP #${scopeId}`
    case 'l4_rule': return `L4 #${scopeId}`
    case 'relay_listener': return `Relay #${scopeId}`
    default: return `${scopeType} #${scopeId}`
  }
}
</script>

<style scoped>
.dashboard-traffic {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  margin-bottom: 2.5rem;
}
.dashboard-traffic__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.dashboard-traffic__title {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}
.dashboard-traffic__select {
  padding: 0.35rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  font-family: inherit;
  cursor: pointer;
}
.dashboard-traffic__loading {
  display: flex;
  justify-content: center;
  padding: 2rem;
}
.dashboard-traffic__cards {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 0.75rem;
  padding: 1rem 1.25rem;
}
.dashboard-traffic__card {
  min-width: 0;
  padding: 0.75rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
}
.dashboard-traffic__card--alert {
  background: var(--color-danger-50);
}
.dashboard-traffic__card-label {
  display: block;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  margin-bottom: 0.25rem;
}
.dashboard-traffic__card-value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1.125rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.dashboard-traffic__card-percent {
  display: block;
  font-size: 0.875rem;
  font-weight: 600;
  margin-top: 0.25rem;
}
.dashboard-traffic__card-percent--success { color: var(--color-success); }
.dashboard-traffic__card-percent--warning { color: var(--color-warning); }
.dashboard-traffic__card-percent--danger { color: var(--color-danger); }
.dashboard-traffic__card-track {
  height: 4px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
  margin-top: 0.375rem;
}
.dashboard-traffic__card-fill {
  height: 100%;
  border-radius: var(--radius-full);
  transition: width 0.3s;
}
.dashboard-traffic__card-fill--success { background: var(--color-success); }
.dashboard-traffic__card-fill--warning { background: var(--color-warning); }
.dashboard-traffic__card-fill--danger { background: var(--color-danger); }
.dashboard-traffic__card-sub {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin-top: 0.25rem;
}
.dashboard-traffic__chart {
  padding: 0 1.25rem;
}
.dashboard-traffic__lists {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 1rem;
  padding: 1rem 1.25rem 1.25rem;
}
.dashboard-traffic__list-panel {
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  padding: 0.75rem;
}
.dashboard-traffic__list-title {
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0 0 0.5rem;
}
.dashboard-traffic__list-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  gap: 0.5rem;
  align-items: center;
  padding: 0.4rem 0;
  font-size: 0.8125rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.dashboard-traffic__list-row:last-child { border-bottom: none; }
.dashboard-traffic__list-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
}
.dashboard-traffic__list-value {
  color: var(--color-text-primary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.dashboard-traffic__list-percent {
  color: var(--color-text-muted);
  font-size: 0.75rem;
  min-width: 2.5rem;
  text-align: right;
}
.dashboard-traffic__list-empty {
  text-align: center;
  color: var(--color-text-muted);
  padding: 1rem;
  font-size: 0.8125rem;
  margin: 0;
}
.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
@media (max-width: 900px) {
  .dashboard-traffic__cards { grid-template-columns: repeat(2, 1fr); }
  .dashboard-traffic__lists { grid-template-columns: 1fr; }
}
@media (max-width: 640px) {
  .dashboard-traffic__cards { grid-template-columns: 1fr; }
}
</style>
