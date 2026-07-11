<template>
  <div v-if="visible" class="dashboard-traffic">
    <div class="dashboard-traffic__header">
      <h2 class="dashboard-traffic__title">流量统计</h2>
      <AgentPicker
        :agents="selectableAgents"
        v-model:model-id="selectedAgentId"
        :show-all-option="true"
        all-label="全部节点"
        class="dashboard-traffic__agent-picker"
      />
    </div>

    <div v-if="aggregateQuery.isLoading.value" class="dashboard-traffic__loading">
      <div class="spinner"></div>
    </div>

    <template v-else>
      <!-- Primary: health summary -->
      <div class="dashboard-traffic__primary dt-health-card" :class="{ 'dt-health-card--blocked': blockedCount > 0 }">
        <div class="dt-health-card__header">
          <span class="dt-health-card__badge" :class="healthBadgeClass" data-testid="health-badge">{{ healthBadgeText }}</span>
          <span class="dt-health-card__meta">{{ overviewAgents.length }} 个节点</span>
        </div>
        <div class="dt-health-card__kpi-grid" data-testid="health-kpi-grid">
          <div class="dt-health-card__kpi dt-health-card__kpi--primary">
            <span class="dt-health-card__kpi-label">已用</span>
            <span class="dt-health-card__kpi-value" data-testid="kpi-used">{{ formatBytes(selectedSummary?.used_bytes || 0) }}</span>
            <span class="dt-health-card__kpi-sub" data-testid="kpi-used-sub">{{ usedSubLabel }}</span>
          </div>
          <div class="dt-health-card__kpi dt-health-card__kpi--primary">
            <span class="dt-health-card__kpi-label">剩余</span>
            <span class="dt-health-card__kpi-value" data-testid="kpi-remaining">{{ remainingLabel }}</span>
            <span class="dt-health-card__kpi-sub" data-testid="kpi-remaining-sub">{{ remainingSubLabel }}</span>
          </div>
          <div class="dt-health-card__kpi" :class="{ 'dt-health-card__kpi--danger': blockedCount > 0 }">
            <span class="dt-health-card__kpi-label">阻断</span>
            <span class="dt-health-card__kpi-value" data-testid="kpi-blocked">{{ blockedCount }} / {{ overviewAgents.length }}</span>
            <span class="dt-health-card__kpi-sub" data-testid="kpi-blocked-sub">{{ blockedSubLabel }}</span>
          </div>
        </div>
      </div>

      <!-- Secondary: demoted trend -->
      <div class="dashboard-traffic__secondary dt-section dt-section--demoted">
        <div class="dt-section__header">
          <h3 class="dt-section__title">流量趋势</h3>
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
        </div>
        <div class="dt-section__body">
          <TrafficTrendChart
            :points="trendPoints"
            :granularity="granularity"
            :quota-bytes="selectedSummary?.quota_bytes ?? null"
          />
        </div>
      </div>

      <!-- Tertiary: distribution / cycle / top -->
      <div class="dashboard-traffic__tertiary dt-tertiary-grid">
        <div class="dt-card dt-card--muted">
          <h3 class="dt-card__title">流量分布</h3>
          <TrafficQuotaRing
            :used-bytes="selectedSummary?.used_bytes ?? 0"
            :quota-bytes="selectedSummary?.quota_bytes ?? null"
            :remaining-bytes="selectedSummary?.remaining_bytes ?? null"
            :agents="distributionAgents"
          />
        </div>
        <div class="dt-card dt-card--muted">
          <h3 class="dt-card__title">计费周期</h3>
          <div class="dt-cycle">
            <span class="dt-cycle__value" data-testid="cycle-label">{{ cycleLabel }}</span>
          </div>
          <div class="dt-cycle__meta">
            <span data-testid="direction-label">方向: {{ directionLabel }}</span>
            <span v-if="dailyBudgetText" data-testid="daily-budget">{{ dailyBudgetText }}</span>
          </div>
        </div>
        <div class="dt-card dt-card--muted">
          <h3 class="dt-card__title">Top 规则</h3>
          <div v-for="(rule, i) in topRules" :key="topRuleKey(rule)" class="dt-top-rule" @click="navigateToAgent(rule)">
            <div class="dt-top-rule__info">
              <span class="dt-top-rule__name">{{ rule.label }}</span>
              <span class="dt-top-rule__value">{{ formatBytes(rule.accounted_bytes) }}</span>
            </div>
            <div class="dt-top-rule__bar">
              <div class="dt-top-rule__fill" :style="{ width: topRulePercent(rule) + '%', background: DISTRIBUTION_COLORS[i % DISTRIBUTION_COLORS.length] }" />
            </div>
          </div>
          <p v-if="!topRules.length" class="dt-card__empty">暂无规则数据</p>
        </div>
        <div class="dt-card dt-card--muted">
          <h3 class="dt-card__title">Top 节点</h3>
          <div v-for="(node, i) in topNodes" :key="'right-' + node.agent_id" class="dt-top-item" @click="navigateToAgent(node)">
            <span class="dt-top-item__rank" :style="rankStyle(i)">{{ i + 1 }}</span>
            <span class="dt-top-item__name">{{ node.name || node.agent_id }}</span>
            <span class="dt-top-item__value">{{ formatBytes(node.used_bytes) }}</span>
          </div>
          <p v-if="!topNodes.length" class="dt-card__empty">暂无节点数据</p>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useTrafficAggregate } from '../../hooks/useTrafficAggregate.js'
import { useAgents } from '../../hooks/useAgents.js'
import { fetchSystemInfo } from '../../api'
import TrafficTrendChart from './TrafficTrendChart.vue'
import TrafficQuotaRing from './TrafficQuotaRing.vue'
import AgentPicker from '../../components/AgentPicker.vue'
import { formatBytes, usagePercent } from '../../utils/trafficStats.js'

const router = useRouter()

const { data: agentsData } = useAgents()
const globalAgentNameMap = computed(() => {
  const agents = agentsData.value || []
  const map = {}
  for (const agent of agents) {
    if (agent.id) {
      map[agent.id] = agent.name?.trim() || null
    }
  }
  return map
})

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

const allAgentsQuery = useTrafficAggregate('', trafficStatsEnabled, granularity)
const aggregateQuery = useTrafficAggregate(selectedAgentId, trafficStatsEnabled, granularity)

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
  const agents = aggregateQuery.data.value?.agents ?? []
  if (agents.length) return agents
  if (!import.meta.env.DEV) return []
  return MOCK_AGENTS
})
const selectableAgents = computed(() => {
  const agents = allAgentsQuery.data.value?.agents
  return agents?.length ? agents : overviewAgents.value
})
const trendPoints = computed(() => {
  const pts = aggregateQuery.data.value?.trend
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

const aggregateNodes = computed(() => aggregateQuery.data.value?.top_nodes ?? [])
const distributionAgents = computed(() => {
  const data = aggregateQuery.data.value
  if (!data || !Array.isArray(data.top_nodes)) {
    return selectedAgentId.value && selectedSummary.value ? [selectedSummary.value] : overviewAgents.value
  }

  const overviewById = new Map(overviewAgents.value.map((agent) => [agent.agent_id, agent]))
  const granularAgents = aggregateNodes.value
    .filter((node) => node.agent_id)
    .map((node) => {
      const agent = overviewById.get(node.agent_id)
      return {
        ...(agent || {}),
        agent_id: node.agent_id,
        name: node.name || agent?.name || node.agent_id,
        used_bytes: Number(node.used_bytes) || 0,
        quota_bytes: node.quota_bytes ?? agent?.quota_bytes ?? null,
        remaining_bytes: agent?.remaining_bytes ?? null
      }
    })
  if (granularAgents.length) {
    return granularAgents
  }

  return overviewAgents.value.map((agent) => ({
    ...agent,
    used_bytes: 0
  }))
})

const blockedCount = computed(() => overviewAgents.value.filter(a => a.blocked).length)

const healthBadgeText = computed(() => {
  if (!overviewAgents.value.length) return '无数据'
  if (blockedCount.value > 0) return `${blockedCount.value} 个节点阻断`
  return '正常'
})
const healthBadgeClass = computed(() => {
  if (!overviewAgents.value.length) return 'dt-health-card__badge--muted'
  if (blockedCount.value > 0) return 'dt-health-card__badge--danger'
  return 'dt-health-card__badge--success'
})

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
  const nodes = aggregateNodes.value
  if (nodes.length) return nodes.slice(0, 5)
  if (!import.meta.env.DEV) return []
  const agents = [...overviewAgents.value]
  agents.sort((a, b) => {
    const pa = a.quota_bytes ? a.used_bytes / a.quota_bytes : a.used_bytes
    const pb = b.quota_bytes ? b.used_bytes / b.quota_bytes : b.used_bytes
    return pb - pa
  })
  return agents.slice(0, 5)
})

const topRules = computed(() => (aggregateQuery.data.value?.top_rules ?? []).slice(0, 5))

function navigateToAgent(agent) {
  if (agent?.agent_id) {
    router.push({
      name: 'agent-detail',
      params: { id: agent.agent_id }
    })
  }
}

const DISTRIBUTION_COLORS = ['#60a5fa', '#a78bfa', '#34d399', '#fbbf24', '#f87171', '#22d3ee', '#f472b6']

function rankStyle(index) {
  return { background: DISTRIBUTION_COLORS[index % DISTRIBUTION_COLORS.length] }
}

function topRulePercent(rule) {
  const rules = topRules.value
  if (!rules.length) return 0
  const max = rules[0].accounted_bytes || 1
  return Math.round((rule.accounted_bytes / max) * 100)
}

function topRuleKey(rule) {
  return rule.key || [rule.agent_id, rule.scope_type, rule.scope_id].filter(Boolean).join(':')
}

const remainingLabel = computed(() => {
  if (selectedSummary.value?.remaining_bytes == null) return '无限制'
  return formatBytes(selectedSummary.value.remaining_bytes)
})

const remainingSubLabel = computed(() => {
  if (selectedSummary.value?.remaining_bytes == null) return '未设置额度'
  return '可用额度'
})

const usedSubLabel = computed(() => {
  const quota = selectedSummary.value?.quota_bytes
  if (quota == null) return '—'
  const pct = usagePercent(selectedSummary.value?.used_bytes || 0, quota)
  return `占额度 ${pct ?? '—'}%`
})

const blockedSubLabel = computed(() => {
  if (blockedCount.value === 0) return '无阻断'
  return '需关注'
})

const dailyBudgetText = computed(() => {
  const agents = overviewAgents.value
  if (!agents.length) return ''
  const first = agents[0]
  if (!first.cycle_start || !first.cycle_end) return ''
  const cycleStart = new Date(first.cycle_start)
  const cycleEnd = new Date(first.cycle_end)
  const days = Math.max(1, Math.ceil((cycleEnd - cycleStart) / 86400000))
  const totalQuota = agents.reduce((s, a) => s + (a.quota_bytes || 0), 0)
  if (!totalQuota) return ''
  const daily = Math.round(totalQuota / days)
  return `日均 ${formatBytes(daily)}`
})

function normalizePoints(raw) {
  return (raw || []).map(p => ({
    bucket_start: p.bucket_start,
    bucket_local_start: p.bucket_local_start,
    rx_bytes: Number(p.rx_bytes) || 0,
    tx_bytes: Number(p.tx_bytes) || 0,
    accounted_bytes: Number(p.accounted_bytes) || 0
  }))
}
</script>

<style scoped>
.dashboard-traffic {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  margin-bottom: var(--space-8);
}
.dashboard-traffic__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
  gap: 0.75rem;
}
.dashboard-traffic__title {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
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

/* Primary health card */
.dashboard-traffic__primary {
  margin: 1rem 1.25rem 0;
}
.dt-health-card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 0.875rem 1rem;
  transition: border-color 0.15s ease, background-color 0.15s ease;
}
.dt-health-card--blocked {
  border-color: var(--color-danger, #ef4444);
  background: color-mix(in srgb, var(--color-danger, #ef4444) 6%, var(--color-bg-surface));
}
.dt-health-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}
.dt-health-card__badge {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.2rem 0.5rem;
  border-radius: var(--radius-full);
  font-size: 0.75rem;
  font-weight: 600;
  line-height: 1.25;
}
.dt-health-card__badge--success {
  background: color-mix(in srgb, var(--color-success, #34d399) 14%, transparent);
  color: color-mix(in srgb, var(--color-success, #34d399) 75%, var(--color-text-primary));
}
.dt-health-card__badge--danger {
  background: color-mix(in srgb, var(--color-danger, #ef4444) 14%, transparent);
  color: var(--color-danger, #ef4444);
}
.dt-health-card__badge--muted {
  background: var(--color-bg-subtle);
  color: var(--color-text-muted);
}
.dt-health-card__meta {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
.dt-health-card__kpi-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(0, 1.1fr) minmax(0, 0.9fr);
  gap: 0.375rem 0.625rem;
  align-items: stretch;
}
.dt-health-card__kpi {
  min-width: 0;
  padding: 0.5rem 0.625rem;
  border-radius: var(--radius-md);
  transition: background-color 0.15s ease;
}
.dt-health-card__kpi:hover {
  background: var(--color-bg-subtle);
}
.dt-health-card__kpi--primary {
  background: var(--color-bg-subtle);
}
.dt-health-card__kpi--primary:hover {
  background: color-mix(in srgb, var(--color-bg-subtle) 80%, var(--color-bg-hover));
}
.dt-health-card__kpi--danger .dt-health-card__kpi-value {
  color: var(--color-danger, #ef4444);
}
.dt-health-card__kpi-label {
  display: block;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 500;
  margin-bottom: 0.2rem;
}
.dt-health-card__kpi-value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1.1875rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.25;
}
.dt-health-card__kpi-sub {
  display: block;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  margin-top: 0.2rem;
  line-height: 1.35;
}

/* Secondary demoted section */
.dashboard-traffic__secondary {
  margin: 1rem 1.25rem 0;
}
.dt-section--demoted {
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 0.875rem 1rem;
  opacity: 0.96;
}
.dt-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  margin-bottom: 0.625rem;
}
.dt-section__title {
  font-size: 0.7rem;
  font-weight: 600;
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin: 0;
}
.dt-section__body {
  min-height: 0;
}

/* Tertiary grid */
.dashboard-traffic__tertiary {
  margin: 1rem 1.25rem;
}
.dt-tertiary-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1rem;
}

.dt-card {
  background: var(--color-bg-surface-raised, var(--color-bg-subtle));
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 0.875rem;
  min-width: 0;
}
.dt-card--muted {
  background: var(--color-bg-subtle);
  border-color: var(--color-border-subtle);
  opacity: 0.95;
}
.dt-card__title {
  font-size: 0.7rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin: 0 0 0.625rem;
}
.dt-card__empty {
  text-align: center;
  color: var(--color-text-muted);
  padding: 1rem;
  font-size: 0.8125rem;
  margin: 0;
}

.dt-cycle__value {
  display: block;
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin-bottom: 0.25rem;
}
.dt-cycle__meta {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}

.dt-top-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.35rem 0;
  font-size: 0.8125rem;
  border-bottom: 1px solid var(--color-border-subtle);
  cursor: pointer;
  transition: background 150ms;
}
.dt-top-item:hover {
  background: var(--color-bg-hover);
  border-radius: var(--radius-sm);
  margin: 0 -0.25rem;
  padding-left: 0.25rem;
  padding-right: 0.25rem;
}
.dt-top-item:last-child { border-bottom: none; }
.dt-top-item__rank {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 0.65rem;
  font-weight: 700;
  color: var(--color-text-inverse);
  flex-shrink: 0;
}
.dt-top-item__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
}
.dt-top-item__value {
  color: var(--color-text-secondary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.dt-top-rule {
  padding: 0.35rem 0;
  border-bottom: 1px solid var(--color-border-subtle);
  cursor: pointer;
  transition: background 150ms;
}
.dt-top-rule:hover {
  background: var(--color-bg-hover);
  border-radius: var(--radius-sm);
  margin: 0 -0.25rem;
  padding-left: 0.25rem;
  padding-right: 0.25rem;
}
.dt-top-rule:last-child { border-bottom: none; }
.dt-top-rule__info {
  display: flex;
  justify-content: space-between;
  font-size: 0.8125rem;
  margin-bottom: 0.25rem;
}
.dt-top-rule__name {
  color: var(--color-text-primary);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dt-top-rule__value {
  color: var(--color-text-secondary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.dt-top-rule__bar {
  height: 5px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
}
.dt-top-rule__fill {
  height: 100%;
  border-radius: var(--radius-full);
  transition: width 0.3s;
}

.dashboard-traffic__granularity {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--color-bg-surface);
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

.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

@media (max-width: 1100px) {
  .dt-tertiary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 768px) {
  .dt-health-card__kpi-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .dt-health-card__kpi:last-child {
    grid-column: 1 / -1;
  }
  .dt-section__header {
    flex-wrap: wrap;
  }
}

@media (max-width: 640px) {
  .dashboard-traffic {
    overflow-x: auto;
    overflow-y: hidden;
    -webkit-overflow-scrolling: touch;
  }
  .dashboard-traffic__header {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.75rem;
  }
  .dashboard-traffic__agent-picker {
    width: 100%;
    min-width: 0;
  }
  .dashboard-traffic__agent-picker :deep(.agent-picker__trigger) {
    width: 100%;
    max-width: none;
  }
  .dashboard-traffic__primary,
  .dashboard-traffic__secondary,
  .dashboard-traffic__tertiary {
    margin-left: 1rem;
    margin-right: 1rem;
    min-width: 340px;
  }
  .dt-health-card__kpi-grid {
    grid-template-columns: 1fr;
  }
  .dt-health-card__kpi:last-child {
    grid-column: auto;
  }
  .dt-tertiary-grid {
    grid-template-columns: 1fr;
    min-width: 340px;
  }
}
</style>
