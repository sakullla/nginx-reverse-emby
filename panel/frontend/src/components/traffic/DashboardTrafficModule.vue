<template>
  <div v-if="visible" class="dashboard-traffic">
    <div v-if="aggregateQuery.isLoading.value" class="dashboard-traffic__loading">
      <div class="spinner"></div>
    </div>

    <template v-else>
      <!-- Row 1: 趋势主视觉(内嵌 KPI 仪表行) + 右栏集群指标(页面注入) -->
      <div class="dashboard-traffic__row dashboard-traffic__row--main">
        <section class="dt-cell dt-cell--hero">
          <div class="dt-cell__header">
            <h2 class="dt-cell__title">流量趋势</h2>
            <div class="dt-cell__tools">
              <div class="dashboard-traffic__view" role="group" aria-label="趋势视角">
                <button
                  v-for="option in viewOptions"
                  :key="option.value"
                  type="button"
                  class="dashboard-traffic__view-btn"
                  :class="{ 'dashboard-traffic__view-btn--active': trafficView === option.value }"
                  data-testid="trend-view-btn"
                  @click="trafficView = option.value"
                >
                  {{ option.label }}
                </button>
              </div>
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

          <!-- KPI 仪表行：阻断信息仅出现在需关注条 -->
          <div class="dt-kpi-row" data-testid="health-kpi-grid">
            <div class="dt-kpi dt-kpi--status">
              <span class="dt-kpi__badge dt-kpi__badge--success" data-testid="health-badge">概览</span>
              <span class="dt-kpi__meta">{{ overviewAgents.length }} 个节点</span>
            </div>
            <div class="dt-kpi">
              <span class="dt-kpi__label">已用</span>
              <span class="dt-kpi__value" data-testid="kpi-used">{{ formatBytes(selectedSummary?.used_bytes || 0) }}</span>
              <span class="dt-kpi__sub" data-testid="kpi-used-sub">{{ usedSubLabel }}</span>
            </div>
            <div class="dt-kpi">
              <span class="dt-kpi__label">剩余</span>
              <span class="dt-kpi__value" data-testid="kpi-remaining">{{ remainingLabel }}</span>
              <span class="dt-kpi__sub" data-testid="kpi-remaining-sub">{{ remainingSubLabel }}</span>
            </div>
            <div class="dt-kpi">
              <span class="dt-kpi__label">占比</span>
              <span class="dt-kpi__value" data-testid="kpi-usage">{{ usageLabel }}</span>
              <span class="dt-kpi__sub" data-testid="kpi-usage-sub">{{ usageSubLabel }}</span>
            </div>
          </div>

          <div class="dt-cell__body">
            <TrafficTrendChart
              :points="chartPoints"
              :series-points="chartSeriesPoints"
              :granularity="granularity"
              :quota-bytes="isRulesView ? null : (selectedSummary?.quota_bytes ?? null)"
            />
          </div>
        </section>

        <aside class="dt-cell dashboard-traffic__side">
          <slot name="side"></slot>
        </aside>
      </div>

      <!-- Row 2: 流量 TOP(tab) + 节点状态(页面注入),与上行栏比交错 -->
      <div class="dashboard-traffic__row dashboard-traffic__row--secondary">
        <section class="dt-cell dt-cell--top">
          <div class="dt-cell__header">
            <h2 class="dt-cell__title">流量 TOP</h2>
            <span class="dt-top-mode" data-testid="top-mode-label">{{ topModeLabel }}</span>
          </div>

          <template v-if="isRulesView">
            <div v-for="(rule, i) in topRules" :key="topRuleKey(rule)" class="dt-top-rule" @click="navigateToAgent(rule)">
              <div class="dt-top-rule__info">
                <span class="dt-top-rule__name">{{ rule.label }}</span>
                <span class="dt-top-rule__value">{{ formatBytes(rule.accounted_bytes) }}</span>
              </div>
              <div class="dt-top-rule__bar">
                <div class="dt-top-rule__fill" :style="{ width: topRulePercent(rule) + '%', background: DISTRIBUTION_COLORS[i % DISTRIBUTION_COLORS.length] }" />
              </div>
            </div>
            <p v-if="!topRules.length" class="dt-cell__empty">暂无规则数据</p>
          </template>

          <template v-else>
            <div v-for="(node, i) in topNodes" :key="'top-node-' + node.agent_id" class="dt-top-item" @click="navigateToAgent(node)">
              <span class="dt-top-item__rank" :style="rankStyle(i)">{{ i + 1 }}</span>
              <span class="dt-top-item__name">{{ node.name || node.agent_id }}</span>
              <span class="dt-top-item__value">{{ formatBytes(node.used_bytes) }}</span>
            </div>
            <p v-if="!topNodes.length" class="dt-cell__empty">暂无节点数据</p>
          </template>
        </section>

        <section class="dt-cell dashboard-traffic__nodes">
          <slot name="nodes"></slot>
        </section>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useTrafficAggregate } from '../../hooks/useTrafficAggregate.js'
import { usePreference } from '../../hooks/usePreference.js'
import { getPreferenceDefinition, normalizeTrafficView } from '../../preferences/definitions.js'
import { fetchSystemInfo } from '../../api'
import TrafficTrendChart from './TrafficTrendChart.vue'
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
const viewDef = getPreferenceDefinition('dashboard.trafficView')
const trafficView = usePreference('dashboard.trafficView', viewDef?.defaultValue || 'nodes')
// 兼容旧值 business → rules
if (trafficView.value === 'business') trafficView.value = 'rules'
const viewOptions = viewDef?.options || [
  { value: 'nodes', label: '按节点' },
  { value: 'rules', label: '按规则' }
]
const activeTrafficView = computed(() => normalizeTrafficView(trafficView.value, 'nodes'))
const isRulesView = computed(() => activeTrafficView.value === 'rules')
const topModeLabel = computed(() => (isRulesView.value ? '规则' : '节点'))

const allAgentsQuery = useTrafficAggregate('', trafficStatsEnabled, granularity)
// 节点 / 规则视角都跟随 AgentPicker 过滤
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
const CATEGORY_LABELS = {
  http_rule: 'HTTP',
  l4_rule: 'L4',
  relay_listener: 'Relay'
}

const trendPoints = computed(() => {
  const pts = aggregateQuery.data.value?.trend
  if (pts?.length) return normalizePoints(pts)
  if (import.meta.env.DEV) return normalizePoints(MOCK_TREND)
  return []
})

function hasCategoryData(points) {
  return (points || []).some((point) => {
    const accounted = Number(point?.accounted_bytes) || 0
    const rx = Number(point?.rx_bytes) || 0
    const tx = Number(point?.tx_bytes) || 0
    return accounted > 0 || rx > 0 || tx > 0
  })
}

const categorySeriesPoints = computed(() => {
  const raw = aggregateQuery.data.value?.category_trend
  if (Array.isArray(raw) && raw.length) {
    return raw.map((entry) => ({
      name: CATEGORY_LABELS[entry.category] || entry.category || '系列',
      category: entry.category,
      points: normalizePoints(entry.points)
    })).filter((entry) => hasCategoryData(entry.points))
  }
  if (import.meta.env.DEV && trendPoints.value.length) {
    // DEV fallback: derive three category curves from total trend so rules view is demoable
    return [
      { name: 'HTTP', category: 'http_rule', points: scalePoints(trendPoints.value, 0.55) },
      { name: 'L4', category: 'l4_rule', points: scalePoints(trendPoints.value, 0.3) },
      { name: 'Relay', category: 'relay_listener', points: scalePoints(trendPoints.value, 0.15) }
    ].filter((entry) => hasCategoryData(entry.points))
  }
  return []
})

const chartPoints = computed(() => (isRulesView.value ? [] : trendPoints.value))
const chartSeriesPoints = computed(() => (isRulesView.value ? categorySeriesPoints.value : null))

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

const usageLabel = computed(() => {
  const quota = selectedSummary.value?.quota_bytes
  if (quota == null) return '—'
  const pct = usagePercent(selectedSummary.value?.used_bytes || 0, quota)
  return pct == null ? '—' : `${pct}%`
})

const usageSubLabel = computed(() => {
  if (selectedSummary.value?.quota_bytes == null) return '未设置额度'
  return '已用 / 额度'
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

function scalePoints(points, factor) {
  return (points || []).map((p) => ({
    ...p,
    rx_bytes: Math.round((Number(p.rx_bytes) || 0) * factor),
    tx_bytes: Math.round((Number(p.tx_bytes) || 0) * factor),
    accounted_bytes: Math.round((Number(p.accounted_bytes) || 0) * factor)
  }))
}
</script>

<style scoped>
.dashboard-traffic {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  margin-bottom: var(--space-4);
}

.dashboard-traffic__row {
  display: grid;
  gap: var(--space-4);
  align-items: stretch;
}

.dashboard-traffic__row--main {
  grid-template-columns: minmax(0, 2.2fr) minmax(0, 1fr);
}

/* 与上行交错:TOP 窄格在左,节点状态宽格在右 */
.dashboard-traffic__row--secondary {
  grid-template-columns: minmax(0, 1fr) minmax(0, 2.2fr);
}

/* Bento cell chrome */
.dt-cell {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-xs);
  padding: var(--space-4) var(--space-5);
  min-width: 0;
}

.dt-cell--hero {
  display: flex;
  flex-direction: column;
}

.dt-cell__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.dt-cell__title {
  font-size: var(--text-base);
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}

.dt-cell__tools {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.dt-cell__body {
  flex: 1;
  min-height: 0;
}

/* Hero 格内给趋势图受控高度(不用 flex 填充,避免 ResizeObserver 反馈循环) */
.dt-cell--hero .dt-cell__body :deep(.traffic-trend-chart) {
  height: clamp(14rem, 22vw, 20rem);
}

.dt-cell--top {
  display: flex;
  flex-direction: column;
}

.dt-cell__empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-muted);
  padding: var(--space-6) 0;
  font-size: var(--text-sm);
  margin: 0;
}

/* KPI 仪表行 */
.dt-kpi-row {
  display: grid;
  grid-template-columns: auto repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
  align-items: stretch;
  padding: var(--space-3);
  margin-bottom: var(--space-3);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
  transition: background-color 0.15s ease;
}

.dt-kpi {
  min-width: 0;
  padding: 0 var(--space-3);
  border-left: 1px solid var(--color-border-subtle);
}

.dt-kpi--status {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 0.35rem;
  border-left: none;
  padding-left: 0;
}

.dt-kpi__badge {
  display: inline-flex;
  align-items: center;
  padding: 0.2rem 0.55rem;
  border-radius: var(--radius-full);
  font-size: 0.75rem;
  font-weight: 600;
  line-height: 1.25;
  width: fit-content;
}

.dt-kpi__badge--success {
  background: color-mix(in srgb, var(--color-success, #34d399) 14%, transparent);
  color: color-mix(in srgb, var(--color-success, #34d399) 75%, var(--color-text-primary));
}

.dt-kpi__badge--muted {
  background: var(--color-bg-surface);
  color: var(--color-text-muted);
}

.dt-kpi__meta {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}

.dt-kpi__label {
  display: block;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 500;
  margin-bottom: 0.2rem;
}

.dt-kpi__value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1.25rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.25;
  animation: dt-kpi-pop 0.45s var(--ease-default, ease) both;
}

.dt-kpi__sub {
  display: block;
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  margin-top: 0.2rem;
}

@keyframes dt-kpi-pop {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}

/* TOP 模式标签（跟随趋势视角） */
.dt-top-mode {
  display: inline-flex;
  align-items: center;
  padding: 0.2rem 0.55rem;
  border-radius: var(--radius-full);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 600;
}

/* TOP 列表 */
.dt-top-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.45rem 0;
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
.dt-top-item:last-of-type { border-bottom: none; }
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
  padding: 0.45rem 0;
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
.dt-top-rule:last-of-type { border-bottom: none; }
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
  transition: width 0.45s var(--ease-default, ease);
  animation: dt-bar-enter 0.55s var(--ease-default, ease) both;
}

@keyframes dt-bar-enter {
  from { transform: scaleX(0); transform-origin: left center; opacity: 0.4; }
  to { transform: scaleX(1); transform-origin: left center; opacity: 1; }
}

/* Agent picker / view / granularity */
.dashboard-traffic__view {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
}

.dashboard-traffic__view-btn {
  min-width: 3.5rem;
  padding: 0.3rem 0.55rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
  font-family: inherit;
  transition: color var(--duration-fast) var(--ease-default),
    background-color var(--duration-fast) var(--ease-default);
}

.dashboard-traffic__view-btn--active {
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

.dashboard-traffic__loading {
  display: flex;
  justify-content: center;
  padding: 2rem;
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

@media (max-width: 1024px) {
  .dashboard-traffic__row--main,
  .dashboard-traffic__row--secondary {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .dt-cell__header {
    flex-direction: column;
    align-items: flex-start;
  }
  .dt-cell__tools {
    width: 100%;
    justify-content: space-between;
  }
  .dashboard-traffic__agent-picker {
    min-width: 0;
  }
  .dt-kpi-row {
    grid-template-columns: 1fr;
    gap: var(--space-2);
  }
  .dt-kpi {
    border-left: none;
    padding: 0;
  }
}
</style>
