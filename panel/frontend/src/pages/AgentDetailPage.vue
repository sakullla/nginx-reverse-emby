<template>
  <div class="agent-detail" v-if="agent">
    <div class="agent-detail__back">
      <RouterLink to="/agents" class="back-link">← 返回节点管理</RouterLink>
    </div>

    <div class="agent-detail__header">
      <div>
        <h1 class="agent-detail__name">{{ agent.name }}</h1>
        <p class="agent-detail__url">{{ agent.agent_url || agent.last_seen_ip || '—' }}</p>
      </div>
      <div class="agent-detail__status" :class="`agent-detail__status--${getStatus(agent)}`">
        {{ getStatusLabel(agent) }}
      </div>
    </div>

    <div class="agent-detail__stats">
      <div class="stat-mini">
        <span class="stat-mini__value">{{ httpRulesCount }}</span>
        <span class="stat-mini__label">HTTP 规则</span>
      </div>
      <div class="stat-mini">
        <span class="stat-mini__value">{{ l4RulesCount }}</span>
        <span class="stat-mini__label">L4 规则</span>
      </div>
      <div class="stat-mini">
        <span class="stat-mini__value">{{ agent.last_seen_at ? timeAgo(agent.last_seen_at) : '—' }}</span>
        <span class="stat-mini__label">最后活跃</span>
      </div>
    </div>

    <div v-if="agent.last_apply_status === 'failed' && agent.last_apply_message" class="agent-detail__error">
      <div class="error-block">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="8" x2="12" y2="12"/>
          <line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
        <div class="error-block__content">
          <div class="error-block__title">同步失败</div>
          <div class="error-block__text">{{ agent.last_apply_message }}</div>
        </div>
      </div>
    </div>

    <div class="agent-detail__tabs">
      <button v-for="tab in tabs" :key="tab.id" class="tab-btn" :class="{ 'tab-btn--active': activeTab === tab.id }" @click="activeTab = tab.id">{{ tab.label }}</button>
    </div>

    <div class="agent-detail__tab-content">
      <div v-if="activeTab === 'traffic'" class="tab-panel">
        <section class="traffic-section">
          <h3 class="traffic-section__title">概览</h3>
          <TrafficSummaryCards :summary="trafficSummary" :direction="trafficPolicyForm.direction" />
          <div class="traffic-tab__trend">
            <div class="traffic-tab__trend-header">
              <span>趋势</span>
              <div class="traffic-trend__controls" role="group" aria-label="趋势粒度">
                <button
                  v-for="option in trafficTrendGranularityOptions"
                  :key="option.value"
                  class="traffic-trend__mode"
                  :class="{ 'traffic-trend__mode--active': trafficTrendGranularity === option.value }"
                  type="button"
                  @click="trafficTrendGranularity = option.value"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
            <TrafficTrendChart :points="trafficTrendPoints" :granularity="trafficTrendGranularity" :quota-bytes="trafficSummary.monthly_quota_bytes ?? null" />
          </div>
          <div class="traffic-tab__breakdown">
            <span class="traffic-tab__breakdown-title">分项流量（点击查看趋势）</span>
            <TrafficBreakdownTable :tabs="trafficBreakdownTabs" :clickable="true" @click-row="openBreakdownTrendModal" />
          </div>
        </section>

        <section class="traffic-section">
          <h3 class="traffic-section__title">策略设置</h3>
          <TrafficPolicyForm v-model="trafficPolicyForm" :saving="updateTrafficPolicyMutation.isPending.value || updateAgent.isPending.value" @save="saveTrafficPolicy" />
        </section>

        <section class="traffic-section">
          <h3 class="traffic-section__title">历史管理</h3>
          <TrafficHistoryManager
            :policy="trafficPolicyForm"
            :calibrating="calibrateTrafficMutation.isPending.value"
            :cleaning="cleanupTrafficMutation.isPending.value"
            @calibrate="calibrateModalVisible = true"
            @calibrate-zero="showCalibrateZeroConfirm"
            @cleanup="showCleanupConfirm"
          />
        </section>

        <TrafficTrendModal
          v-model:visible="trendModal.visible"
          :agent-id="agentId"
          :scope-type="trendModal.scopeType"
          :scope-id="trendModal.scopeId"
          :scope-label="trendModal.scopeLabel"
          :direction="trafficPolicyForm.direction"
        />
        <TrafficCalibrateModal
          v-model:visible="calibrateModalVisible"
          :agent-id="agentId"
          :current-used-bytes="trafficSummary.used_bytes ?? 0"
          :cycle-start="trafficSummary.cycle_start ?? ''"
          :cycle-end="trafficSummary.cycle_end ?? ''"
          @confirm="onCalibrateConfirm"
        />
        <DeleteConfirmDialog
          :show="confirmDialog.visible"
          :title="confirmDialog.title"
          :message="confirmDialog.message"
          :confirm-text="confirmDialog.confirmText"
          :loading="confirmDialog.loading"
          @confirm="onConfirmDialogConfirm"
          @cancel="confirmDialog.visible = false"
        />
      </div>

      <div v-if="activeTab === 'http'" class="tab-panel">
        <div class="tab-panel__header">
          <button class="btn btn-primary" @click="router.push({ path: '/rules', query: { agentId } })">查看全部规则</button>
        </div>
        <div class="rules-preview">
          <div v-for="rule in httpRules.slice(0, 5)" :key="rule.id" class="rule-preview-item">
            <span class="rule-preview-item__url">{{ rule.frontend_url }}</span>
            <span class="rule-preview-item__backend">→ {{ formatHttpBackend(rule) }}</span>
          </div>
          <p v-if="!httpRules.length" class="empty-hint">暂无 HTTP 规则</p>
        </div>
      </div>

      <div v-if="activeTab === 'l4'" class="tab-panel">
        <div class="tab-panel__header">
          <button class="btn btn-primary" @click="router.push({ path: '/l4', query: { agentId } })">查看全部规则</button>
        </div>
        <div class="rules-preview">
          <div v-for="rule in l4Rules.slice(0, 5)" :key="rule.id" class="rule-preview-item">
            <span class="rule-preview-item__url">{{ rule.listen_host }}:{{ rule.listen_port }}</span>
            <span class="rule-preview-item__backend">→ {{ formatL4Backend(rule) }}</span>
          </div>
          <p v-if="!l4Rules.length" class="empty-hint">暂无 L4 规则</p>
        </div>
      </div>

      <div v-if="activeTab === 'info'" class="tab-panel">
        <div class="info-grid">
          <div class="info-row"><span>版本</span><span>{{ agent.version || agent.runtime_package_version || '—' }}</span></div>
          <div class="info-row"><span>平台</span><span>{{ agent.runtime_package_platform || agent.platform || '—' }}</span></div>
          <div class="info-row"><span>架构</span><span>{{ agent.runtime_package_arch || '—' }}</span></div>
          <div class="info-row"><span>运行包 SHA</span><span :title="agent.runtime_package_sha256 || ''">{{ shortSha(agent.runtime_package_sha256) }}</span></div>
          <div class="info-row"><span>目标包 SHA</span><span :title="agent.desired_package_sha256 || ''">{{ shortSha(agent.desired_package_sha256) }}</span></div>
          <div class="info-row"><span>包状态</span><span>{{ packageStatusLabel(agent.package_sync_status) }}</span></div>
          <div class="info-row"><span>角色</span><span>{{ getModeLabel(agent.mode) }}</span></div>
          <div class="info-row"><span>IP</span><span>{{ agent.last_seen_ip || '—' }}</span></div>
          <div class="info-row"><span>最后活跃</span><span>{{ agent.last_seen_at ? new Date(agent.last_seen_at).toLocaleString() : '—' }}</span></div>
          <div class="info-row"><span>同步状态</span><span>{{ agent.last_apply_status || '—' }}</span></div>
          <div v-if="agent.last_apply_message" class="info-row"><span>同步消息</span><span>{{ agent.last_apply_message }}</span></div>
        </div>
      </div>
    </div>
  </div>
  <div v-else-if="isLoading" class="agent-detail__loading">
    <div class="spinner"></div>
  </div>
  <div v-else class="agent-detail__not-found">
    <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
      <circle cx="12" cy="12" r="10"/>
      <line x1="12" y1="8" x2="12" y2="12"/>
      <line x1="12" y1="16" x2="12.01" y2="16"/>
    </svg>
    <p>节点不存在或已删除</p>
    <RouterLink to="/agents" class="btn btn-secondary">返回节点管理</RouterLink>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useRules } from '../hooks/useRules'
import { useL4Rules } from '../hooks/useL4Rules'
import { useAgents, useUpdateAgent } from '../hooks/useAgents'
import { fetchAgentStats, fetchSystemInfo } from '../api'
import { useCalibrateTraffic, useCleanupTraffic, useTrafficPolicy, useTrafficSummary, useTrafficTrend, useUpdateTrafficPolicy } from '../hooks/useTraffic'
import { messageStore } from '../stores/messages'
import { buildOutboundProxyPayload } from './outboundProxyURL'
import {
  accountedBytes,
  formatBytes,
  formatQuota,
  normalizeTrafficBucket,
  normalizeTrafficPolicy,
  normalizeTrafficTrendPoints
} from '../utils/trafficStats.js'
import TrafficTrendChart from '../components/traffic/TrafficTrendChart.vue'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
import TrafficSummaryCards from '../components/traffic/TrafficSummaryCards.vue'
import TrafficBreakdownTable from '../components/traffic/TrafficBreakdownTable.vue'
import TrafficPolicyForm from '../components/traffic/TrafficPolicyForm.vue'
import TrafficHistoryManager from '../components/traffic/TrafficHistoryManager.vue'
import TrafficCalibrateModal from '../components/traffic/TrafficCalibrateModal.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'

const route = useRoute()
const router = useRouter()
const agentId = computed(() => route.params.id)

const { data: agentsData, isLoading } = useAgents()
const agent = computed(() => agentsData.value?.find(a => a.id === agentId.value))
const updateAgent = useUpdateAgent()
const outboundProxyURL = ref('')

const { data: httpRulesData } = useRules(agentId)
const httpRules = computed(() => httpRulesData.value ?? [])
const httpRulesCount = computed(() => httpRules.value.length)

const { data: l4RulesData } = useL4Rules(agentId)
const l4Rules = computed(() => l4RulesData.value ?? [])
const l4RulesCount = computed(() => l4Rules.value.length)

const { data: agentStatsData } = useQuery({
  queryKey: ['agent-stats', agentId],
  queryFn: () => fetchAgentStats(agentId.value),
  enabled: () => !!agentId.value,
  refetchInterval: 10_000
})
const { data: systemInfoData, isSuccess: isSystemInfoLoaded } = useQuery({
  queryKey: ['system-info'],
  queryFn: fetchSystemInfo
})
const agentStats = computed(() => agentStatsData.value ?? {})
const systemInfo = computed(() => systemInfoData.value ?? {})
const trafficStatsEnabled = computed(() => isSystemInfoLoaded.value && systemInfo.value?.traffic_stats_enabled !== false)
const trafficPolicyQuery = useTrafficPolicy(computed(() => trafficStatsEnabled.value ? agentId.value : null))
const trafficSummaryQuery = useTrafficSummary(computed(() => trafficStatsEnabled.value ? agentId.value : null))
const trafficTrendGranularityOptions = [
  { value: 'hour', label: '小时' },
  { value: 'day', label: '日' },
  { value: 'month', label: '月' }
]
const trafficTrendGranularity = ref('day')
const trafficTrendQuery = useTrafficTrend(
  computed(() => trafficStatsEnabled.value ? agentId.value : null),
  computed(() => ({ granularity: trafficTrendGranularity.value }))
)
const updateTrafficPolicyMutation = useUpdateTrafficPolicy(computed(() => agentId.value))
const calibrateTrafficMutation = useCalibrateTraffic(computed(() => agentId.value))
const cleanupTrafficMutation = useCleanupTraffic(computed(() => agentId.value))
const quotaUnits = [
  { value: 'B', label: 'B', factor: 1 },
  { value: 'KiB', label: 'KiB', factor: 1024 },
  { value: 'MiB', label: 'MiB', factor: 1024 ** 2 },
  { value: 'GiB', label: 'GiB', factor: 1024 ** 3 },
  { value: 'TiB', label: 'TiB', factor: 1024 ** 4 }
]
const trafficPolicyForm = ref(normalizeTrafficPolicyForm())
const trafficSummary = computed(() => trafficSummaryQuery.data.value ?? {})
const trafficTrendPoints = computed(() => normalizeTrafficTrendPoints(trafficTrendQuery.data.value ?? [], trafficPolicyForm.value.direction))
const trafficBreakdownTabs = computed(() => [
  {
    id: 'http',
    label: 'HTTP',
    rows: normalizeTrafficBreakdownRows(trafficSummary.value.http_rules)
  },
  {
    id: 'l4',
    label: 'L4',
    rows: normalizeTrafficBreakdownRows(trafficSummary.value.l4_rules)
  },
  {
    id: 'relay',
    label: 'Relay',
    rows: normalizeTrafficBreakdownRows(trafficSummary.value.relay_listeners)
  },
  {
    id: 'host',
    label: '主机接口',
    rows: normalizeTrafficBreakdownRows([
      ...(trafficSummary.value.host_total ? [trafficSummary.value.host_total] : []),
      ...(trafficSummary.value.host_interfaces || [])
    ])
  }
].filter(t => t.rows.length > 0))

const activeTab = ref('http')
const trendModal = ref({ visible: false, scopeType: '', scopeId: '', scopeLabel: '' })
const calibrateModalVisible = ref(false)
const confirmDialog = ref({ visible: false, type: '', title: '', message: '', confirmText: '', loading: false })

function openBreakdownTrendModal(row) {
  trendModal.value = {
    visible: true,
    scopeType: row.scope_type,
    scopeId: row.scope_id,
    scopeLabel: trafficBreakdownLabel(row)
  }
}

const tabs = computed(() => [
  { id: 'http', label: 'HTTP 规则' },
  { id: 'l4', label: 'L4 规则' },
  ...(trafficStatsEnabled.value ? [{ id: 'traffic', label: '流量统计' }] : []),
  { id: 'info', label: '系统信息' }
])

watch(agent, (value) => {
  outboundProxyURL.value = value?.outbound_proxy_url || ''
  if (value) {
    trafficPolicyForm.value = {
      ...trafficPolicyForm.value,
      traffic_stats_interval: value.traffic_stats_interval || ''
    }
  }
}, { immediate: true })

watch([trafficPolicyQuery.data, trafficStatsEnabled], ([policy, enabled]) => {
  if (enabled && policy) {
    trafficPolicyForm.value = normalizeTrafficPolicyForm(policy, agent.value?.traffic_stats_interval || '')
  }
}, { immediate: true })

watch(tabs, (value) => {
  if (!value.some((tab) => tab.id === activeTab.value)) {
    activeTab.value = value[0]?.id || 'http'
  }
}, { immediate: true })

async function saveOutboundProxy() {
  if (!agent.value || agent.value.is_local) return
  let payload
  try {
    payload = buildOutboundProxyPayload(agent.value.outbound_proxy_url, outboundProxyURL.value)
  } catch (error) {
    messageStore.warning(error.message, '出网代理密码已隐藏')
    return
  }
  if (Object.keys(payload).length === 0) return
  await updateAgent.mutateAsync({
    agentId: agent.value.id,
    payload
  })
}

async function saveTrafficPolicy() {
  if (!agent.value || !trafficStatsEnabled.value) return
  if (!isIntegerInRange(trafficPolicyForm.value.cycle_start_day, 1, 28)) {
    messageStore.warning('月周期起始日必须是 1 到 28 的整数')
    return
  }
  const monthlyQuotaBytes = quotaInputToBytes(trafficPolicyForm.value.monthly_quota_value, trafficPolicyForm.value.monthly_quota_unit)
  if (monthlyQuotaBytes === undefined) {
    messageStore.warning('月额度必须为空或非负数字')
    return
  }
  if (!isPositiveInteger(trafficPolicyForm.value.hourly_retention_days)) {
    messageStore.warning('小时保留必须是正整数')
    return
  }
  if (!isPositiveInteger(trafficPolicyForm.value.daily_retention_months)) {
    messageStore.warning('日保留必须是正整数')
    return
  }
  if (!isBlankOrPositiveInteger(trafficPolicyForm.value.monthly_retention_months)) {
    messageStore.warning('月保留必须为空或正整数')
    return
  }
  const payload = normalizeTrafficPolicy({
    ...trafficPolicyForm.value,
    monthly_quota_bytes: monthlyQuotaBytes
  })
  await updateTrafficPolicyMutation.mutateAsync(payload)

  const nextInterval = String(trafficPolicyForm.value.traffic_stats_interval || '').trim()
  if (!agent.value.is_local && nextInterval !== (agent.value.traffic_stats_interval || '')) {
    await updateAgent.mutateAsync({
      agentId: agent.value.id,
      payload: { traffic_stats_interval: nextInterval }
    })
  }
}

async function onCalibrateConfirm(usedBytes) {
  if (!agent.value || !trafficStatsEnabled.value) return
  await calibrateTrafficMutation.mutateAsync({ used_bytes: usedBytes })
}

async function calibrateTrafficSummary() {
  if (!agent.value || !trafficStatsEnabled.value) return
  calibrateModalVisible.value = true
}

function showCalibrateZeroConfirm() {
  if (!agent.value || !trafficStatsEnabled.value) return
  confirmDialog.value = {
    visible: true,
    type: 'calibrate-zero',
    title: '确认归零',
    message: '将当前计费周期的已用流量重置为零，此操作不可撤销。',
    confirmText: '确认归零',
    loading: false
  }
}

function showCleanupConfirm() {
  if (!agent.value || !trafficStatsEnabled.value) return
  confirmDialog.value = {
    visible: true,
    type: 'cleanup',
    title: '确认清理',
    message: '按保留策略清理过期历史数据，此操作不可撤销。',
    confirmText: '确认清理',
    loading: false
  }
}

async function onConfirmDialogConfirm() {
  if (!agent.value || !trafficStatsEnabled.value) return
  confirmDialog.value.loading = true
  try {
    if (confirmDialog.value.type === 'calibrate-zero') {
      await calibrateTrafficMutation.mutateAsync({ used_bytes: 0 })
    } else if (confirmDialog.value.type === 'cleanup') {
      await cleanupTrafficMutation.mutateAsync()
    }
  } finally {
    confirmDialog.value.visible = false
    confirmDialog.value.loading = false
  }
}

function normalizeTrafficPolicyForm(policy = {}, trafficStatsInterval = '') {
  const normalized = normalizeTrafficPolicy(policy)
  const quota = bytesToQuotaInput(normalized.monthly_quota_bytes)
  return {
    ...normalized,
    monthly_quota_value: quota.value,
    monthly_quota_unit: quota.unit,
    traffic_stats_interval: trafficStatsInterval
  }
}

function bytesToQuotaInput(bytes) {
  if (bytes == null) {
    return { value: '', unit: 'GiB' }
  }
  const number = Number(bytes)
  if (!Number.isFinite(number) || number < 0) {
    return { value: '', unit: 'GiB' }
  }
  let selectedUnit = quotaUnits[0]
  for (const unit of quotaUnits) {
    if (number >= unit.factor) {
      selectedUnit = unit
    }
  }
  const value = number / selectedUnit.factor
  return {
    value: Number.isInteger(value) ? String(value) : String(Number(value.toFixed(3))),
    unit: selectedUnit.value
  }
}

function quotaInputToBytes(value, unitValue) {
  const rawValue = String(value ?? '').trim()
  if (rawValue === '') return null
  const number = Number(rawValue)
  const unit = quotaUnits.find((item) => item.value === unitValue)
  if (!Number.isFinite(number) || number < 0 || !unit) return undefined
  const bytes = number * unit.factor
  if (!Number.isFinite(bytes) || bytes < 0) return undefined
  return Math.round(bytes)
}

function parseByteInput(value) {
  const rawValue = String(value ?? '').trim()
  if (rawValue === '') return undefined
  const match = rawValue.match(/^([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?i?b)?$/i)
  if (!match) return undefined
  const number = Number(match[1])
  const unitValue = normalizeByteUnit(match[2] || 'B')
  const unit = quotaUnits.find((item) => item.value === unitValue)
  if (!Number.isFinite(number) || number < 0 || !unit) return undefined
  const bytes = number * unit.factor
  if (!Number.isFinite(bytes) || bytes < 0) return undefined
  return Math.round(bytes)
}

function normalizeByteUnit(value) {
  switch (String(value || '').trim().toLowerCase()) {
    case 'b':
      return 'B'
    case 'kib':
    case 'kb':
      return 'KiB'
    case 'mib':
    case 'mb':
      return 'MiB'
    case 'gib':
    case 'gb':
      return 'GiB'
    case 'tib':
    case 'tb':
      return 'TiB'
    default:
      return ''
  }
}

function normalizeTrafficBreakdownRows(rows) {
  if (!Array.isArray(rows)) return []
  return rows.map((row) => ({
    scope_type: String(row?.scope_type || ''),
    scope_id: String(row?.scope_id || ''),
    rx_bytes: Number(row?.rx_bytes) || 0,
    tx_bytes: Number(row?.tx_bytes) || 0,
    accounted_bytes: Number(row?.accounted_bytes) || 0
  })).filter((row) => row.accounted_bytes > 0 || row.rx_bytes > 0 || row.tx_bytes > 0)
}

function trafficBreakdownKey(row) {
  return `${row.scope_type || 'scope'}-${row.scope_id || 'aggregate'}`
}

function trafficBreakdownLabel(row) {
  switch (row.scope_type) {
    case 'http':
      return 'HTTP'
    case 'l4':
      return 'L4'
    case 'relay':
      return 'Relay'
    case 'http_rule':
      return `HTTP 规则 #${row.scope_id}`
    case 'l4_rule':
      return `L4 规则 #${row.scope_id}`
    case 'relay_listener':
      return `Relay 监听 #${row.scope_id}`
    default:
      return row.scope_id ? `${row.scope_type} #${row.scope_id}` : row.scope_type || '-'
  }
}

function trafficDirectionLabel(direction) {
  switch (String(direction || 'both').toLowerCase()) {
    case 'rx':
      return '入站'
    case 'tx':
      return '出站'
    case 'max':
      return '取最大值'
    case 'both':
    default:
      return '双向'
  }
}

function isBlankOrPositiveInteger(value) {
  if (value == null || value === '') return true
  return isPositiveInteger(value)
}

function isPositiveInteger(value) {
  const number = Number(value)
  return Number.isInteger(number) && number > 0
}

function isIntegerInRange(value, min, max) {
  const number = Number(value)
  return Number.isInteger(number) && number >= min && number <= max
}

function formatCycle(start, end) {
  if (!start || !end) return '—'
  return `${new Date(start).toLocaleDateString()} - ${new Date(end).toLocaleDateString()}`
}

function formatTrendLabel(bucketStart) {
  if (!bucketStart) return '—'
  const date = new Date(bucketStart)
  if (Number.isNaN(date.getTime())) return '—'
  if (trafficTrendGranularity.value === 'hour') {
    return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }
  if (trafficTrendGranularity.value === 'month') {
    return date.toLocaleDateString('zh-CN', { year: '2-digit', month: 'short' })
  }
  return date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}

function trafficTrendKey(point, index) {
  return `${point.bucket_start || 'point'}-${index}`
}

function trendBarHeight(bytes) {
  const value = Number(bytes) || 0
  const max = Math.max(...trafficTrendPoints.value.map((point) => point.accounted_bytes), 1)
  const ratio = Math.max(0.08, value / max)
  return `${Math.round(ratio * 100)}%`
}

function firstHttpBackend(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    const first = String(rule.backends[0]?.url || '').trim()
    if (first) return first
  }
  return String(rule?.backend_url || '').trim()
}

function formatHttpBackend(rule) {
  const first = firstHttpBackend(rule)
  const count = Array.isArray(rule?.backends) && rule.backends.length > 0 ? rule.backends.length : (first ? 1 : 0)
  if (!first) return '-'
  return count > 1 ? `${first} +${count - 1}` : first
}

function firstL4Backend(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    const backend = rule.backends[0]
    if (backend?.host && backend?.port) return `${backend.host}:${backend.port}`
  }
  if (rule?.upstream_host && rule?.upstream_port) {
    return `${rule.upstream_host}:${rule.upstream_port}`
  }
  return ''
}

function formatL4Backend(rule) {
  const first = firstL4Backend(rule)
  const count = Array.isArray(rule?.backends) && rule.backends.length > 0 ? rule.backends.length : (first ? 1 : 0)
  if (!first) return '-'
  return count > 1 ? `${first} +${count - 1}` : first
}

function getStatus(agent) {
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if (agent.desired_revision > agent.current_revision) return 'pending'
  return 'online'
}

function getStatusLabel(agent) {
  const map = { online: '在线', offline: '离线', failed: '失败', pending: '同步中' }
  return map[getStatus(agent)] || '—'
}

function getModeLabel(mode) {
  return { local: '本机', master: '主控' }[mode] || '拉取'
}

function shortSha(value) {
  const sha = String(value || '').trim()
  if (!sha) return '—'
  return sha.length > 12 ? `${sha.slice(0, 12)}...` : sha
}

function packageStatusLabel(status) {
  if (status === 'aligned') return '已同步'
  if (status === 'pending') return '待更新'
  return '—'
}

function timeAgo(date) {
  const seconds = Math.floor((Date.now() - new Date(date)) / 1000)
  if (seconds < 60) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}
</script>

<style scoped>
.agent-detail { max-width: 900px; margin: 0 auto; }
.agent-detail__back { margin-bottom: 1.5rem; }
.back-link { color: var(--color-text-secondary); font-size: 0.875rem; text-decoration: none; }
.back-link:hover { color: var(--color-primary); }
.agent-detail__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 1.5rem; }
.agent-detail__name { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.agent-detail__url { font-size: 0.875rem; color: var(--color-text-tertiary); font-family: var(--font-mono); margin: 0; }
.agent-detail__status { font-size: 0.8rem; font-weight: 600; padding: 0.25rem 0.75rem; border-radius: var(--radius-full); }
.agent-detail__status--online { background: var(--color-success-50); color: var(--color-success); }
.agent-detail__status--offline { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.agent-detail__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.agent-detail__status--pending { background: var(--color-warning-50); color: var(--color-warning); }
.agent-detail__stats { display: flex; gap: 1rem; margin-bottom: 1.5rem; }
.stat-mini { flex: 1; background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1rem; text-align: center; }
.stat-mini__value { display: block; font-size: 1.5rem; font-weight: 700; color: var(--color-text-primary); }
.stat-mini__label { font-size: 0.75rem; color: var(--color-text-tertiary); }
.traffic-summary { margin-bottom: 1.5rem; padding: 1rem; background: var(--color-bg-surface); border: 1px solid var(--color-border-default); border-radius: var(--radius-lg); }
.agent-detail__tabs { display: flex; gap: 2px; margin-bottom: 1.5rem; padding: 3px; background: var(--color-bg-subtle); border: 1px solid var(--color-border-default); border-radius: var(--radius-lg); }
.tab-btn { padding: 6px 1rem; border: none; background: transparent; color: var(--color-text-muted); font-size: 0.875rem; font-weight: 500; cursor: pointer; border-radius: var(--radius-md); transition: all 0.15s; font-family: inherit; flex: 1; text-align: center; white-space: nowrap; }
.tab-btn:hover { color: var(--color-text-secondary); }
.tab-btn--active { color: var(--color-primary); background: var(--color-bg-surface); font-weight: 600; box-shadow: var(--shadow-sm); }
.tab-panel__header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; margin-bottom: 1rem; }
.tab-panel__title-group h2 { margin: 0; font-size: 1rem; color: var(--color-text-primary); }
.tab-panel__title-group span { color: var(--color-text-tertiary); font-size: 0.8125rem; }
.tab-panel__actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.rules-preview { display: flex; flex-direction: column; gap: 0.5rem; }
.rule-preview-item { display: flex; gap: 0.75rem; padding: 0.75rem 1rem; background: var(--color-bg-surface); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); font-size: 0.8125rem; }
.rule-preview-item__url { flex: 1; color: var(--color-text-primary); font-family: var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-preview-item__backend { color: var(--color-text-tertiary); font-family: var(--font-mono); }
.traffic-tab__trend { margin-bottom: 1rem; }
.traffic-tab__trend-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 0.75rem; font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); }
.traffic-tab__breakdown { margin-top: 0.5rem; }
.traffic-tab__breakdown-title { display: block; font-size: 0.8125rem; color: var(--color-text-tertiary); margin-bottom: 0.5rem; }
.traffic-trend__controls { display: inline-flex; gap: 2px; padding: 2px; background: var(--color-bg-subtle); border: 1px solid var(--color-border-default); border-radius: var(--radius-md); }
.traffic-trend__mode { min-width: 2.75rem; padding: 0.3rem 0.55rem; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--color-text-tertiary); font-size: 0.75rem; font-weight: 600; cursor: pointer; font-family: inherit; }
.traffic-trend__mode--active { background: var(--color-bg-surface); color: var(--color-primary); box-shadow: var(--shadow-sm); }
.empty-hint { text-align: center; color: var(--color-text-muted); padding: 2rem; font-size: 0.875rem; }
.info-grid { display: flex; flex-direction: column; gap: 0.5rem; }
.info-row { display: flex; justify-content: space-between; padding: 0.75rem 1rem; background: var(--color-bg-surface); border-radius: var(--radius-lg); font-size: 0.875rem; }
.info-row span:first-child { color: var(--color-text-secondary); }
.info-row span:last-child { color: var(--color-text-primary); font-weight: 500; }
.agent-detail__error { margin-bottom: 1.5rem; }
.error-block { display: flex; align-items: flex-start; gap: 0.75rem; padding: 1rem; background: var(--color-danger-50); border: 1px solid var(--color-danger-100); border-radius: var(--radius-lg); color: var(--color-danger); }
.error-block svg { flex-shrink: 0; margin-top: 1px; }
.error-block__title { font-weight: 600; font-size: 0.875rem; margin-bottom: 0.25rem; }
.error-block__text { font-size: 0.8125rem; line-height: 1.5; color: var(--color-danger); opacity: 0.95; word-break: break-word; }
.agent-detail__loading { display: flex; justify-content: center; padding: 3rem; }
.agent-detail__not-found { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 1rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.agent-detail__not-found p { margin: 0; font-size: 1rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.btn { padding: 10px 24px; border-radius: var(--radius-full); font-size: var(--text-sm); font-weight: var(--font-semibold); cursor: pointer; transition: all var(--duration-fast) var(--ease-default); border: 1.5px solid transparent; font-family: inherit; display: inline-flex; align-items: center; justify-content: center; gap: 0.375rem; }
.btn-primary { background: var(--color-primary); color: white; }
.btn-primary:hover { background: var(--color-primary-hover); }
.btn-secondary { background: transparent; color: var(--color-text-secondary); border: 1.5px solid var(--color-border-default); }
.btn-secondary:hover { border-color: var(--color-primary); color: var(--color-primary); background: var(--color-primary-subtle); }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
.traffic-section { margin-bottom: 1.5rem; }
.traffic-section__title { font-size: 1rem; font-weight: 600; color: var(--color-text-primary); margin: 0 0 0.75rem; }
@media (max-width: 720px) {
  .agent-detail__header,
  .tab-panel__header { flex-direction: column; }
  .agent-detail__tabs { overflow-x: auto; }
  .tab-btn { flex: 0 0 auto; }
}
</style>
