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
        <div class="tab-panel__header">
          <div class="tab-panel__title-group">
            <h2>流量统计</h2>
            <span>{{ agentStats?.status || '—' }}</span>
          </div>
          <div class="tab-panel__actions">
            <button class="btn btn-secondary" type="button" :disabled="calibrateTrafficMutation.isPending.value" @click="calibrateTrafficSummary">校准</button>
            <button class="btn btn-secondary" type="button" :disabled="cleanupTrafficMutation.isPending.value" @click="cleanupTrafficHistory">清理</button>
          </div>
        </div>

        <div class="traffic-summary__cards">
          <div class="traffic-total">
            <span class="traffic-total__label">月额度</span>
            <span class="traffic-total__value">{{ formatQuota(trafficSummary.monthly_quota_bytes) }}</span>
          </div>
          <div class="traffic-total">
            <span class="traffic-total__label">已用</span>
            <span class="traffic-total__value">{{ formatBytes(trafficSummary.used_bytes) }}</span>
          </div>
          <div class="traffic-total">
            <span class="traffic-total__label">剩余</span>
            <span class="traffic-total__value">{{ trafficSummary.remaining_bytes == null ? '无限制' : formatBytes(trafficSummary.remaining_bytes) }}</span>
          </div>
          <div class="traffic-total">
            <span class="traffic-total__label">周期</span>
            <span class="traffic-total__value">{{ trafficSummary.cycle_start ? formatCycle(trafficSummary.cycle_start, trafficSummary.cycle_end) : '—' }}</span>
          </div>
        </div>

        <div class="traffic-panel">
          <div class="traffic-panel__section">
            <div class="traffic-panel__section-header">
              <h3>趋势</h3>
              <span>{{ trafficTrendPoints.length }} 天</span>
            </div>
            <div class="traffic-trend">
              <div v-for="(point, index) in trafficTrendPoints" :key="trafficTrendKey(point, index)" class="traffic-trend__item">
                <div class="traffic-trend__bars">
                  <div class="traffic-trend__bar traffic-trend__bar--rx" :style="{ height: trendBarHeight(point.rx_bytes) }"></div>
                  <div class="traffic-trend__bar traffic-trend__bar--tx" :style="{ height: trendBarHeight(point.tx_bytes) }"></div>
                </div>
                <span class="traffic-trend__label">{{ formatTrendLabel(point.bucket_start) }}</span>
              </div>
            </div>
          </div>

          <div class="traffic-panel__section">
            <div class="traffic-panel__section-header">
              <h3>月额度</h3>
              <span>{{ trafficSummary.blocked ? '已阻断' : '正常' }}</span>
            </div>
            <div class="traffic-policy-grid">
              <label class="traffic-setting">
                <span class="traffic-setting__label">方向</span>
                <select v-model="trafficPolicyForm.direction" class="traffic-setting__input">
                  <option value="both">双向</option>
                  <option value="rx">入站</option>
                  <option value="tx">出站</option>
                  <option value="max">取最大值</option>
                </select>
              </label>
              <label class="traffic-setting">
                <span class="traffic-setting__label">月周期起始日</span>
                <input v-model.number="trafficPolicyForm.cycle_start_day" class="traffic-setting__input" type="number" min="1" max="28">
              </label>
              <label class="traffic-setting">
                <span class="traffic-setting__label">月额度</span>
                <input v-model="trafficPolicyForm.monthly_quota_bytes" class="traffic-setting__input" type="text" placeholder="留空表示无限制">
              </label>
              <label class="traffic-setting traffic-setting--switch">
                <span class="traffic-setting__label">超额阻断</span>
                <input v-model="trafficPolicyForm.block_when_exceeded" type="checkbox">
              </label>
              <label class="traffic-setting">
                <span class="traffic-setting__label">小时保留</span>
                <input v-model.number="trafficPolicyForm.hourly_retention_days" class="traffic-setting__input" type="number" min="1">
              </label>
              <label class="traffic-setting">
                <span class="traffic-setting__label">日保留</span>
                <input v-model.number="trafficPolicyForm.daily_retention_months" class="traffic-setting__input" type="number" min="1">
              </label>
              <label class="traffic-setting">
                <span class="traffic-setting__label">月保留</span>
                <input v-model="trafficPolicyForm.monthly_retention_months" class="traffic-setting__input" type="number" min="1" placeholder="留空表示永久">
              </label>
            </div>
            <div class="traffic-panel__footer">
              <button class="btn btn-primary" type="button" :disabled="updateTrafficPolicyMutation.isPending.value" @click="saveTrafficPolicy">保存</button>
            </div>
          </div>
        </div>
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
        <div v-if="!agent.is_local" class="agent-setting">
          <label class="agent-setting__label" for="agent-outbound-proxy">出网代理</label>
          <div class="agent-setting__control">
            <input
              id="agent-outbound-proxy"
              v-model="outboundProxyURL"
              class="agent-setting__input"
              placeholder="socks://user:pass@127.0.0.1:1080"
            >
            <button
              class="btn btn-primary"
              type="button"
              :disabled="updateAgent.isPending.value"
              @click="saveOutboundProxy"
            >
              保存
            </button>
          </div>
        </div>
        <div v-if="!agent.is_local" class="agent-setting">
          <label class="agent-setting__label" for="agent-traffic-stats-interval">流量统计上报周期</label>
          <div class="agent-setting__control">
            <input
              id="agent-traffic-stats-interval"
              v-model="trafficStatsInterval"
              class="agent-setting__input"
              placeholder="例如 30s、1m、5m；留空表示随心跳上报"
            >
            <button
              class="btn btn-primary"
              type="button"
              :disabled="updateAgent.isPending.value"
              @click="saveTrafficStatsInterval"
            >
              保存
            </button>
          </div>
        </div>
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

const route = useRoute()
const router = useRouter()
const agentId = computed(() => route.params.id)

const { data: agentsData, isLoading } = useAgents()
const agent = computed(() => agentsData.value?.find(a => a.id === agentId.value))
const updateAgent = useUpdateAgent()
const outboundProxyURL = ref('')
const trafficStatsInterval = ref('')

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
const trafficTrendQuery = useTrafficTrend(
  computed(() => trafficStatsEnabled.value ? agentId.value : null),
  computed(() => ({ granularity: 'day' }))
)
const updateTrafficPolicyMutation = useUpdateTrafficPolicy(computed(() => agentId.value))
const calibrateTrafficMutation = useCalibrateTraffic(computed(() => agentId.value))
const cleanupTrafficMutation = useCleanupTraffic(computed(() => agentId.value))
const trafficPolicyForm = ref(normalizeTrafficPolicy())
const trafficSummary = computed(() => trafficSummaryQuery.data.value ?? {})
const trafficTrendPoints = computed(() => normalizeTrafficTrendPoints(trafficTrendQuery.data.value ?? [], trafficPolicyForm.value.direction))

const activeTab = ref('http')
const tabs = computed(() => [
  { id: 'http', label: 'HTTP 规则' },
  { id: 'l4', label: 'L4 规则' },
  ...(trafficStatsEnabled.value ? [{ id: 'traffic', label: '流量统计' }] : []),
  { id: 'info', label: '系统信息' }
])

watch(agent, (value) => {
  outboundProxyURL.value = value?.outbound_proxy_url || ''
  trafficStatsInterval.value = value?.traffic_stats_interval || ''
}, { immediate: true })

watch([trafficPolicyQuery.data, trafficStatsEnabled], ([policy, enabled]) => {
  if (enabled && policy) {
    trafficPolicyForm.value = {
      ...normalizeTrafficPolicy(policy),
      monthly_quota_bytes: policy.monthly_quota_bytes == null ? '' : String(policy.monthly_quota_bytes)
    }
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

async function saveTrafficStatsInterval() {
  if (!agent.value || agent.value.is_local) return
  const nextInterval = trafficStatsInterval.value.trim()
  if (nextInterval === (agent.value.traffic_stats_interval || '')) return
  await updateAgent.mutateAsync({
    agentId: agent.value.id,
    payload: { traffic_stats_interval: nextInterval }
  })
}

async function saveTrafficPolicy() {
  if (!agent.value || !trafficStatsEnabled.value) return
  if (!isIntegerInRange(trafficPolicyForm.value.cycle_start_day, 1, 28)) {
    messageStore.warning('月周期起始日必须是 1 到 28 的整数')
    return
  }
  if (!isBlankOrFiniteNonNegative(trafficPolicyForm.value.monthly_quota_bytes)) {
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
    monthly_quota_bytes: trafficPolicyForm.value.monthly_quota_bytes === '' ? null : trafficPolicyForm.value.monthly_quota_bytes
  })
  await updateTrafficPolicyMutation.mutateAsync(payload)
}

async function calibrateTrafficSummary() {
  if (!agent.value || !trafficStatsEnabled.value) return
  const usedBytes = trafficSummary.value.used_bytes ?? accountedBytes(normalizeTrafficBucket(agentStats.value?.traffic?.total), trafficPolicyForm.value.direction)
  await calibrateTrafficMutation.mutateAsync({ used_bytes: usedBytes })
}

async function cleanupTrafficHistory() {
  if (!agent.value || !trafficStatsEnabled.value) return
  if (typeof window !== 'undefined' && !window.confirm('确认清理当前保留策略之外的流量历史？')) return
  await cleanupTrafficMutation.mutateAsync()
}

function isBlankOrFiniteNonNegative(value) {
  if (value == null || value === '') return true
  const number = Number(value)
  return Number.isFinite(number) && number >= 0
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
  return date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}

function trafficTrendKey(point, index) {
  return `${point.bucket_start || 'point'}-${index}`
}

function trendBarHeight(bytes) {
  const value = Number(bytes) || 0
  const max = Math.max(...trafficTrendPoints.value.map((point) => Math.max(point.rx_bytes, point.tx_bytes)), 1)
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
.traffic-summary__cards { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 0.75rem; margin-bottom: 1rem; }
.traffic-total { min-width: 0; padding: 0.75rem; background: var(--color-bg-subtle); border-radius: var(--radius-md); }
.traffic-total__label { display: block; margin-bottom: 0.25rem; color: var(--color-text-tertiary); font-size: 0.75rem; }
.traffic-total__value { display: block; color: var(--color-text-primary); font-size: 1.25rem; font-weight: 700; font-variant-numeric: tabular-nums; }
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
.traffic-panel { display: grid; gap: 1rem; }
.traffic-panel__section { padding: 1rem; background: var(--color-bg-surface); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); }
.traffic-panel__section-header { display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; margin-bottom: 0.75rem; }
.traffic-panel__section-header h3 { margin: 0; font-size: 0.9375rem; color: var(--color-text-primary); }
.traffic-panel__section-header span { color: var(--color-text-tertiary); font-size: 0.8125rem; }
.traffic-trend { display: grid; grid-template-columns: repeat(14, minmax(0, 1fr)); gap: 0.35rem; align-items: end; min-height: 140px; }
.traffic-trend__item { display: flex; flex-direction: column; align-items: center; gap: 0.35rem; min-width: 0; }
.traffic-trend__bars { display: flex; align-items: end; gap: 0.2rem; width: 100%; height: 120px; padding: 0 0.125rem; }
.traffic-trend__bar { flex: 1; min-height: 6px; border-radius: var(--radius-sm) var(--radius-sm) 0 0; }
.traffic-trend__bar--rx { background: var(--color-primary-200); }
.traffic-trend__bar--tx { background: var(--color-primary); }
.traffic-trend__label { color: var(--color-text-tertiary); font-size: 0.6875rem; text-align: center; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; width: 100%; }
.traffic-policy-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.75rem; }
.traffic-setting { display: flex; flex-direction: column; gap: 0.35rem; min-width: 0; }
.traffic-setting--switch { flex-direction: row; align-items: center; justify-content: space-between; }
.traffic-setting__label { color: var(--color-text-secondary); font-size: 0.8125rem; font-weight: 500; }
.traffic-setting__input { width: 100%; min-width: 0; padding: 0.5rem 0.75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); font-size: 0.875rem; box-sizing: border-box; }
.traffic-setting__input:focus { outline: none; border-color: var(--color-primary); box-shadow: var(--shadow-focus); }
.traffic-panel__footer { display: flex; justify-content: flex-end; margin-top: 0.75rem; }
.empty-hint { text-align: center; color: var(--color-text-muted); padding: 2rem; font-size: 0.875rem; }
.info-grid { display: flex; flex-direction: column; gap: 0.5rem; }
.agent-setting { display: flex; flex-direction: column; gap: 0.5rem; margin-bottom: 1rem; padding: 1rem; background: var(--color-bg-surface); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); }
.agent-setting__label { color: var(--color-text-secondary); font-size: 0.875rem; font-weight: 500; }
.agent-setting__control { display: flex; gap: 0.5rem; align-items: center; }
.agent-setting__input { flex: 1; min-width: 0; padding: 0.5rem 0.75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); font-size: 0.875rem; font-family: var(--font-mono); box-sizing: border-box; }
.agent-setting__input:focus { outline: none; border-color: var(--color-primary); box-shadow: var(--shadow-focus); }
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
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
@media (max-width: 720px) {
  .traffic-summary__cards,
  .traffic-policy-grid { grid-template-columns: 1fr; }
  .agent-detail__header,
  .tab-panel__header { flex-direction: column; }
  .agent-detail__tabs { overflow-x: auto; }
  .tab-btn { flex: 0 0 auto; }
}
</style>
