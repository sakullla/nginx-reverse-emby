<template>
  <div v-if="visible" class="dashboard-traffic">
    <div class="dashboard-traffic__header">
      <h2 class="dashboard-traffic__title">流量统计</h2>
      <div class="dashboard-traffic__toolbar">
        <div class="dashboard-traffic__granularity" role="group" aria-label="趋势粒度">
          <button
            v-for="option in granularityOptions"
            :key="option.value"
            type="button"
            class="dashboard-traffic__granularity-btn"
            :class="{ 'dashboard-traffic__granularity-btn--active': granularity === option.value }"
            @click="granularity = option.value"
          >
            {{ option.label }}
          </button>
        </div>
        <AgentPicker
          :agents="selectableAgents"
          v-model:model-id="selectedAgentId"
          :show-all-option="true"
          all-label="全部节点"
          class="dashboard-traffic__agent-picker"
        />
      </div>
    </div>

    <div v-if="overviewQuery.isLoading.value" class="dashboard-traffic__loading">
      <div class="spinner"></div>
    </div>

    <template v-else>
      <!-- Bento Grid -->
      <div class="dashboard-traffic__bento">
        <!-- 趋势图 -->
        <div class="bento-card bento-card--trend">
          <TrafficTrendChart
            :points="trendPoints"
            :granularity="granularity"
            :quota-bytes="selectedSummary?.quota_bytes ?? null"
          />
        </div>

        <!-- 配额环形图 -->
        <div class="bento-card bento-card--quota">
          <TrafficQuotaRing
            :used-bytes="selectedSummary?.used_bytes ?? 0"
            :quota-bytes="selectedSummary?.quota_bytes ?? null"
            :remaining-bytes="selectedSummary?.remaining_bytes ?? null"
            :agents="selectedAgentId && selectedSummary ? [selectedSummary] : overviewAgents"
          />
        </div>

        <!-- 实时速率 -->
        <div class="bento-card bento-card--rate">
          <TrafficRateSparkline :points="trendPoints" :granularity="granularity" />
        </div>

        <!-- 阻断节点 -->
        <div class="bento-card bento-card--blocked" :class="{ 'bento-card--alert': blockedCount > 0 }">
          <span class="bento-card__label">阻断节点</span>
          <span class="bento-card__value">{{ blockedCount }} / {{ overviewAgents.length }}</span>
          <span v-if="blockedCount > 0" class="bento-card__sub bento-card__sub--alert">{{ blockedCount }} 个节点已超额阻断</span>
          <span v-else class="bento-card__sub">所有节点正常</span>
        </div>

        <!-- 计费周期 -->
        <div class="bento-card bento-card--cycle">
          <span class="bento-card__label">计费周期</span>
          <span class="bento-card__value">{{ cycleLabel }}</span>
          <span class="bento-card__sub">方向: {{ directionLabel }}</span>
        </div>

        <!-- Top 节点 -->
        <div class="bento-card bento-card--top-nodes">
          <h3 class="bento-card__title">Top 节点</h3>
          <div v-for="agent in topNodes" :key="agent.agent_id" class="top-row top-row--clickable" @click="navigateToAgent(agent)">
            <span class="top-row__name">{{ agent.name || agent.agent_id }}</span>
            <div class="top-row__bar-track">
              <div class="top-row__bar-fill" :style="{ width: topNodePercent(agent) + '%' }" />
            </div>
            <span class="top-row__value">{{ formatBytes(agent.used_bytes) }}</span>
          </div>
          <p v-if="!topNodes.length" class="bento-card__empty">暂无节点数据</p>
        </div>

        <!-- Top 规则 -->
        <div class="bento-card bento-card--top-rules">
          <h3 class="bento-card__title">Top 规则</h3>
          <div v-for="rule in topRules" :key="rule.key" class="top-row top-row--clickable" @click="navigateToAgentByRule(rule)">
            <span class="top-row__name" :title="rule.label">{{ rule.label }}</span>
            <div class="top-row__bar-track">
              <div class="top-row__bar-fill" :style="{ width: rule.percent + '%' }" />
            </div>
            <span class="top-row__value">{{ formatBytes(rule.accounted_bytes) }}</span>
          </div>
          <p v-if="!topRules.length" class="bento-card__empty">暂无规则数据</p>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useTrafficOverview } from '../../hooks/useTrafficOverview.js'
import { fetchSystemInfo, fetchTrafficSummary } from '../../api'
import TrafficTrendChart from './TrafficTrendChart.vue'
import TrafficQuotaRing from './TrafficQuotaRing.vue'
import TrafficRateSparkline from './TrafficRateSparkline.vue'
import AgentPicker from '../../components/AgentPicker.vue'
import { formatBytes, usagePercent } from '../../utils/trafficStats.js'

const router = useRouter()

const { data: systemInfo } = useQuery({
  queryKey: ['system-info'],
  queryFn: fetchSystemInfo
})

const trafficStatsEnabled = computed(() => !!systemInfo.value && systemInfo.value.traffic_stats_enabled !== false)
const visible = trafficStatsEnabled

const selectedAgentId = ref('')
const granularity = ref('day')
const granularityOptions = [
  { value: 'hour', label: '小时' },
  { value: 'day', label: '日' },
  { value: 'month', label: '月' }
]

const overviewQuery = useTrafficOverview(selectedAgentId, trafficStatsEnabled, granularity)
const allAgentsQuery = useTrafficOverview('', trafficStatsEnabled, granularity)

const MOCK_AGENTS = [
  { agent_id: 'mock-1', name: '节点-A', used_bytes: 1073741824, quota_bytes: 2147483648, remaining_bytes: 1073741824, direction: 'both', cycle_start: '2026-05-01', cycle_end: '2026-06-01', blocked: false },
  { agent_id: 'mock-2', name: '节点-B', used_bytes: 536870912, quota_bytes: 1073741824, remaining_bytes: 536870912, direction: 'both', cycle_start: '2026-05-01', cycle_end: '2026-06-01', blocked: false },
  { agent_id: 'mock-3', name: '节点-C', used_bytes: 3221225472, quota_bytes: 3221225472, remaining_bytes: 0, direction: 'both', cycle_start: '2026-05-01', cycle_end: '2026-06-01', blocked: true }
]

const MOCK_TREND = Array.from({ length: 30 }, (_, i) => {
  const day = String(i + 1).padStart(2, '0')
  return {
    bucket_start: `2026-05-${day}T00:00:00Z`,
    rx_bytes: Math.round(Math.random() * 80000000 + 20000000),
    tx_bytes: Math.round(Math.random() * 60000000 + 10000000),
    accounted_bytes: Math.round(Math.random() * 100000000 + 50000000)
  }
})

const overviewAgents = computed(() => {
  const agents = overviewQuery.data.value?.agents ?? []
  if (agents.length) return agents
  if (!import.meta.env.DEV) return []
  return MOCK_AGENTS
})
const selectableAgents = computed(() => {
  const agents = allAgentsQuery.data.value?.agents
  return agents?.length ? agents : overviewAgents.value
})
const trendPoints = computed(() => {
  const pts = overviewQuery.data.value?.trend
  if (pts?.length) return normalizePoints(pts)
  if (import.meta.env.DEV) return normalizePoints(MOCK_TREND)
  return []
})

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

const blockedCount = computed(() => overviewAgents.value.filter(a => a.blocked).length)

const cycleLabel = computed(() => {
  const agents = overviewAgents.value
  if (!agents.length) return '—'
  const cycles = new Set(agents.map(a => [
    a.direction || 'both',
    a.cycle_start || '',
    a.cycle_end || ''
  ].join('|')))
  return cycles.size === 1 ? (agents[0].cycle_start || '—') : '多节点混合'
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
  return agents.slice(0, 5)
})

function topNodePercent(agent) {
  if (!agent.quota_bytes || agent.quota_bytes <= 0) {
    const max = Math.max(...topNodes.value.map(a => a.used_bytes), 1)
    return Math.round((agent.used_bytes / max) * 100)
  }
  return Math.min(100, usagePercent(agent.used_bytes, agent.quota_bytes))
}

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
    for (const [index, summary] of summaries.entries()) {
      if (!summary) continue
      const agentId = ids[index]
      const agentName = overviewAgents.value.find(a => a.agent_id === agentId)?.name || agentId
      for (const list of [summary.http_rules, summary.l4_rules, summary.relay_listeners]) {
        if (!Array.isArray(list)) continue
        for (const row of list) {
          const key = `${agentId}-${row.scope_type}-${row.scope_id}`
          const existing = ruleMap.get(key)
          if (existing) {
            existing.accounted_bytes += row.accounted_bytes || 0
            existing.rx_bytes += row.rx_bytes || 0
            existing.tx_bytes += row.tx_bytes || 0
          } else {
            const label = `${agentName} / ${scopeLabel(row.scope_type, row.scope_id)}`
            ruleMap.set(key, { key, agent_id: agentId, label, accounted_bytes: row.accounted_bytes || 0, rx_bytes: row.rx_bytes || 0, tx_bytes: row.tx_bytes || 0 })
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
    return rules.slice(0, 5)
  },
  enabled: computed(() => overviewAgents.value.length > 0 && visible.value)
})

const topRules = computed(() => topRulesQuery.data.value ?? [])

function navigateToAgent(agent) {
  if (agent?.agent_id) {
    router.push({
      name: 'agent-detail',
      params: { id: agent.agent_id }
    })
  }
}

function navigateToAgentByRule(rule) {
  const agentId = rule?.agent_id
  if (agentId) {
    router.push({
      name: 'agent-detail',
      params: { id: agentId }
    })
  }
}

function normalizePoints(raw) {
  return (raw || []).map(p => ({
    bucket_start: p.bucket_start,
    bucket_local_start: p.bucket_local_start,
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
.dashboard-traffic__toolbar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.dashboard-traffic__granularity {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
}
.dashboard-traffic__granularity-btn {
  min-width: 2.75rem;
  padding: 0.3rem 0.55rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
  font-family: inherit;
}
.dashboard-traffic__granularity-btn--active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}
.dashboard-traffic__agent-picker {
  flex-shrink: 0;
}
.dashboard-traffic__agent-picker :deep(.agent-picker__trigger) {
  min-width: 120px;
  padding: 0.35rem 0.65rem;
  font-size: 0.8125rem;
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  gap: 0.35rem;
}
.dashboard-traffic__agent-picker :deep(.agent-picker__trigger-text) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 140px;
}
.dashboard-traffic__loading {
  display: flex;
  justify-content: center;
  padding: 2rem;
}

.dashboard-traffic__bento {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  grid-template-rows: 280px 120px auto;
  grid-template-areas:
    "trend trend quota"
    "rate blocked cycle"
    "top-nodes top-nodes top-rules";
  gap: 1rem;
  padding: 1rem 1.25rem 1.25rem;
}

.bento-card {
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  padding: 0.75rem;
  min-width: 0;
}
.bento-card--trend { grid-area: trend; padding: 0.5rem; }
.bento-card--quota { grid-area: quota; }
.bento-card--rate { grid-area: rate; }
.bento-card--blocked { grid-area: blocked; }
.bento-card--cycle { grid-area: cycle; }
.bento-card--top-nodes { grid-area: top-nodes; }
.bento-card--top-rules { grid-area: top-rules; }

.bento-card--alert {
  background: var(--color-danger-50);
  border: 1px solid var(--color-danger-100);
}

.bento-card__label {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin-bottom: 0.25rem;
}
.bento-card__value {
  display: block;
  font-size: 1.125rem;
  font-weight: 700;
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}
.bento-card__sub {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin-top: 0.25rem;
}
.bento-card__sub--alert { color: var(--color-danger); }
.bento-card__title {
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0 0 0.5rem;
}
.bento-card__empty {
  text-align: center;
  color: var(--color-text-muted);
  padding: 1rem;
  font-size: 0.8125rem;
  margin: 0;
}

.top-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  gap: 0.5rem;
  align-items: center;
  padding: 0.35rem 0;
  font-size: 0.8125rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.top-row:last-child { border-bottom: none; }
.top-row--clickable {
  cursor: pointer;
  transition: background 150ms;
}
.top-row--clickable:hover {
  background: var(--color-bg-hover, rgba(0,0,0,0.03));
  border-radius: var(--radius-sm);
  margin: 0 -0.25rem;
  padding-left: 0.25rem;
  padding-right: 0.25rem;
}
.top-row__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
}
.top-row__bar-track {
  display: none;
}
.top-row__value {
  color: var(--color-text-primary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
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

@media (max-width: 1023px) {
  .dashboard-traffic__bento {
    grid-template-columns: repeat(2, 1fr);
    grid-template-rows: 260px 260px auto auto;
    grid-template-areas:
      "trend trend"
      "quota rate"
      "blocked cycle"
      "top-nodes top-rules";
  }
}

@media (max-width: 767px) {
  .dashboard-traffic__header {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.75rem;
  }
  .dashboard-traffic__toolbar {
    width: 100%;
    flex-wrap: wrap;
  }
  .dashboard-traffic__agent-picker {
    flex: 1;
    min-width: 0;
  }
  .dashboard-traffic__bento {
    grid-template-columns: 1fr;
    grid-template-rows: auto;
    grid-template-areas:
      "trend"
      "quota"
      "rate"
      "blocked"
      "cycle"
      "top-nodes"
      "top-rules";
  }
  .bento-card--trend { min-height: 220px; }
}
</style>
