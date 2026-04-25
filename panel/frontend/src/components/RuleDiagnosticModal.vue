<template>
  <BaseModal
    :model-value="modelValue"
    :title="title"
    size="xl"
    :close-on-click-modal="!busy"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div class="diagnostic-modal">
      <div class="diagnostic-modal__hero">
        <div class="diagnostic-modal__hero-text">
          <div class="diagnostic-modal__eyebrow">{{ kindLabel }}</div>
          <h3 class="diagnostic-modal__headline">{{ ruleLabel }}</h3>
          <p class="diagnostic-modal__subtitle">{{ endpointLabel }}</p>
          <p v-if="agentLabel" class="diagnostic-modal__meta">节点: {{ agentLabel }}</p>
        </div>
        <span class="diagnostic-modal__state" :class="`diagnostic-modal__state--${tone}`">
          {{ stateLabel }}
        </span>
      </div>

      <div v-if="busy" class="diagnostic-modal__loading">
        <div class="diagnostic-modal__pulse"></div>
        <div>
          <div class="diagnostic-modal__loading-title">正在从节点执行实探</div>
          <div class="diagnostic-modal__loading-text">这会直接测试实际链路延迟和丢包，不是静态估算。</div>
        </div>
      </div>

      <div v-else-if="task?.error" class="diagnostic-modal__error">
        {{ task.error }}
      </div>

      <template v-else-if="summary">
        <div class="diagnostic-modal__stats">
          <div class="diagnostic-stat">
            <span class="diagnostic-stat__label">{{ hasRelayPaths ? '总路径数' : '总测试数' }}</span>
            <strong class="diagnostic-stat__value">{{ hasRelayPaths ? pathStats.total : (summary.sent ?? 0) }}</strong>
          </div>
          <div class="diagnostic-stat diagnostic-stat--success">
            <span class="diagnostic-stat__label">{{ hasRelayPaths ? '成功路径' : '成功' }}</span>
            <strong class="diagnostic-stat__value">{{ hasRelayPaths ? pathStats.success : (summary.succeeded ?? 0) }}</strong>
          </div>
          <div class="diagnostic-stat diagnostic-stat--danger">
            <span class="diagnostic-stat__label">{{ hasRelayPaths ? '失败路径' : '失败' }}</span>
            <strong class="diagnostic-stat__value">{{ hasRelayPaths ? pathStats.failed : (summary.failed ?? 0) }}</strong>
          </div>
        </div>

        <!-- Relay Paths -->
        <div v-if="hasRelayPaths" class="diagnostic-section">
          <div class="diagnostic-section__title">Relay 路径探测</div>
          <div v-for="(pathReport, pathIndex) in relayPaths" :key="pathIndex" class="relay-path-card">
            <div class="relay-path-card__header">
              <span class="relay-path-card__name">路径 {{ pathIndex + 1 }}</span>
              <span v-if="pathReport.selected" class="pill pill--success">已选中</span>
              <span :class="`pill pill--${pathReport.success ? 'success' : 'danger'}`">
                {{ pathReport.success ? '成功' : '失败' }}
              </span>
              <span v-if="pathReport.latency_ms" class="relay-path-card__latency">{{ pathReport.latency_ms }} ms</span>
            </div>
            <div class="diagnostic-table">
              <div class="diagnostic-table__header">
                <span>链路</span>
                <span style="text-align:center">状态</span>
                <span style="text-align:center">延迟</span>
                <span style="text-align:center">质量</span>
              </div>
              <div
                v-for="(hop, hopIndex) in pathReport.hops"
                :key="hopIndex"
                class="diagnostic-table__row"
                :class="{ 'diagnostic-table__row--failed': hopIsFailed(hop) }"
              >
                <div class="diagnostic-table__cell">
                  <span :class="hopStatusIconClass(hop)">
                    {{ hopStatusIcon(hop) }}
                  </span>
                  <span>{{ formatHopLabel(hop) }}</span>
                </div>
                <span class="diagnostic-table__cell" style="text-align:center">
                  <span :class="`pill pill--${hopStatusTone(hop)}`">
                    {{ hopStatusLabel(hop) }}
                  </span>
                </span>
                <span class="diagnostic-table__cell" style="text-align:center">
                  <span :class="hopLatencyClass(hop)">
                    {{ hopHasLatency(hop) ? (hop.latency_ms + ' ms') : '—' }}
                  </span>
                </span>
                <span class="diagnostic-table__cell" style="text-align:center">
                  <span :class="`pill pill--${qualityToneFor(hopQualityLabel(hop))}`">
                    {{ hopQualityLabel(hop) }}
                  </span>
                </span>
              </div>
            </div>
            <div v-if="pathReport.error" class="relay-path-card__error">{{ pathReport.error }}</div>
          </div>
        </div>

        <div v-if="backendSummaries.length" class="diagnostic-modal__backends">
          <button type="button" class="diagnostic-modal__section-title diagnostic-modal__section-title--toggle" @click="showBackends = !showBackends">
            <span>后端延迟</span>
            <span v-if="backendSummaries.length" class="diagnostic-modal__sample-count">{{ backendSummaries.length }} 个</span>
            <span class="diagnostic-modal__toggle" :class="{ 'diagnostic-modal__toggle--open': showBackends }">▸</span>
          </button>
          <Transition name="slide-expand">
            <div v-if="showBackends" class="diagnostic-backend-list">
              <article v-for="backend in backendSummaries" :key="backend.backend" class="diagnostic-backend-item">
                <div class="diagnostic-backend-item__header">
                  <code class="diagnostic-backend-item__name">{{ backendDisplayLabel(backend) }}</code>
                  <div class="diagnostic-backend-item__badges">
                    <span v-if="backend.adaptive?.preferred" class="diagnostic-backend-item__preferred">当前优选</span>
                    <span class="diagnostic-backend-item__quality" :class="`diagnostic-backend-item__quality--${qualityToneFor(backend.summary?.quality)}`">
                      {{ qualityLabelFor(backend.summary?.quality) }}
                    </span>
                  </div>
                </div>

                <div class="diagnostic-backend-item__metrics">
                  <div class="diagnostic-metric">
                    <span class="diagnostic-metric__label">延迟</span>
                    <strong class="diagnostic-metric__value">{{ backendActualLatency(backend) }} ms</strong>
                  </div>
                  <div class="diagnostic-metric">
                    <span class="diagnostic-metric__label">稳定性</span>
                    <strong class="diagnostic-metric__value">{{ formatPercent(displayAdaptive(backend)?.stability) }}</strong>
                  </div>
                  <div v-if="showHTTPAdaptiveMetrics" class="diagnostic-metric">
                    <span class="diagnostic-metric__label">综合性能</span>
                    <strong class="diagnostic-metric__value">{{ formatScore(displayAdaptive(backend)?.performance_score) }}</strong>
                  </div>
                  <div v-if="showHTTPAdaptiveMetrics" class="diagnostic-metric">
                    <span class="diagnostic-metric__label">持续吞吐</span>
                    <strong class="diagnostic-metric__value">{{ formatThroughput(displayAdaptive(backend)?.sustained_throughput_bps) }}</strong>
                  </div>
                </div>

                <div class="diagnostic-backend-item__probe">
                  <span class="diagnostic-backend-item__probe-stat">本次测试 <strong>{{ backend.summary?.avg_latency_ms ?? 0 }} ms</strong></span>
                  <span class="diagnostic-backend-item__probe-stat">成功 <strong>{{ backend.summary?.succeeded ?? 0 }} / {{ backend.summary?.sent ?? 0 }}</strong></span>
                </div>

                <button type="button" class="diagnostic-backend-item__toggle" @click="toggleAdaptive(backend.backend)">
                  <span>{{ isAdaptiveExpanded(backend.backend) ? '收起' : '展开更多' }}</span>
                  <span class="diagnostic-backend-item__toggle-icon" :class="{ 'diagnostic-backend-item__toggle-icon--open': isAdaptiveExpanded(backend.backend) }">▸</span>
                </button>

                <Transition name="slide-expand">
                  <div v-if="isAdaptiveExpanded(backend.backend)" class="diagnostic-backend-item__details">
                    <div class="diagnostic-backend-item__details-grid">
                      <div class="diagnostic-factor">
                        <span class="diagnostic-factor__label">延迟</span>
                        <strong class="diagnostic-factor__value">{{ backendActualLatency(backend) }} ms</strong>
                      </div>
                      <div class="diagnostic-factor">
                        <span class="diagnostic-factor__label">近24h成功</span>
                        <strong class="diagnostic-factor__value">{{ displayAdaptive(backend)?.recent_succeeded ?? 0 }}</strong>
                      </div>
                      <div class="diagnostic-factor">
                        <span class="diagnostic-factor__label">近24h失败</span>
                        <strong class="diagnostic-factor__value">{{ displayAdaptive(backend)?.recent_failed ?? 0 }}</strong>
                      </div>
                      <div v-if="showHTTPAdaptiveMetrics" class="diagnostic-factor">
                        <span class="diagnostic-factor__label">持续吞吐</span>
                        <strong class="diagnostic-factor__value">{{ formatThroughput(displayAdaptive(backend)?.sustained_throughput_bps) }}</strong>
                      </div>
                      <div v-if="showHTTPAdaptiveMetrics" class="diagnostic-factor">
                        <span class="diagnostic-factor__label">综合性能</span>
                        <strong class="diagnostic-factor__value">{{ formatScore(displayAdaptive(backend)?.performance_score) }}</strong>
                      </div>
                      <div v-if="showHTTPAdaptiveMetrics" class="diagnostic-factor">
                        <span class="diagnostic-factor__label">异常检测</span>
                        <strong class="diagnostic-factor__value">{{ outlierLabel(displayAdaptive(backend)?.outlier) }}</strong>
                      </div>
                      <div class="diagnostic-factor">
                        <span class="diagnostic-factor__label">流量阶段</span>
                        <strong class="diagnostic-factor__value">{{ trafficShareLabel(displayAdaptive(backend)?.traffic_share_hint) }}</strong>
                      </div>
                    </div>
                    <div v-if="showHTTPAdaptiveMetrics && displayAdaptive(backend)?.reason" class="diagnostic-backend-item__reason">
                      原因: {{ reasonLabel(displayAdaptive(backend)?.reason) }}
                    </div>
                    <div v-if="backend.children?.length" class="diagnostic-backend-item__children">
                      <div class="diagnostic-backend-item__child-title">已解析候选</div>
                      <div class="diagnostic-child-list">
                        <div v-for="child in backend.children" :key="child.backend" class="diagnostic-child-item">
                          <code class="diagnostic-child-item__name">{{ backendDisplayLabel(child) }}</code>
                          <code v-if="backendDisplayAddress(child)" class="diagnostic-child-item__address">{{ backendDisplayAddress(child) }}</code>
                          <span v-if="child.adaptive?.preferred" class="diagnostic-backend-item__preferred">当前优选</span>
                          <span class="diagnostic-child-item__metric">延迟 {{ backendActualLatency(child) }} ms</span>
                          <span class="diagnostic-child-item__metric">稳定性 {{ formatPercent(child.adaptive?.stability) }}</span>
                          <span class="diagnostic-child-item__metric">近24h成功 {{ child.adaptive?.recent_succeeded ?? 0 }}</span>
                        </div>
                      </div>
                    </div>
                  </div>
                </Transition>
              </article>
            </div>
          </Transition>
        </div>

        <div class="diagnostic-modal__samples">
          <button type="button" class="diagnostic-modal__section-title diagnostic-modal__section-title--toggle" @click="showSamples = !showSamples">
            <span>探测样本</span>
            <span v-if="samples.length" class="diagnostic-modal__sample-count">{{ samples.length }} 次</span>
            <span class="diagnostic-modal__toggle" :class="{ 'diagnostic-modal__toggle--open': showSamples }">▸</span>
          </button>
          <div v-if="showSamples" class="diagnostic-sample-list">
            <div class="diagnostic-sample" v-for="sample in samples" :key="`${sample.attempt}-${sample.backend}`" :class="{ 'diagnostic-sample--failed': !sample.success }">
              <div class="diagnostic-sample__left">
                <span class="diagnostic-sample__attempt">#{{ sample.attempt }}</span>
                <span v-if="isHTTP && sample.status_code" class="diagnostic-sample__status" :class="`diagnostic-sample__status--${httpStatusTone(sample.status_code)}`">{{ sample.status_code }}</span>
                <div class="diagnostic-sample__backend-wrap">
                  <code class="diagnostic-sample__backend">{{ backendDisplayLabel(sample) || '-' }}</code>
                  <code v-if="backendDisplayAddress(sample)" class="diagnostic-sample__backend-address">{{ backendDisplayAddress(sample) }}</code>
                </div>
              </div>
              <div class="diagnostic-sample__right">
                <span v-if="sample.success">{{ sample.latency_ms }} ms</span>
                <span v-else>{{ sample.error || '失败' }}</span>
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </BaseModal>
</template>

<script setup>
import { computed, ref } from 'vue'
import BaseModal from './base/BaseModal.vue'
import { diagnosticStateLabel, diagnosticStateTone } from '../hooks/useDiagnostics'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  task: { type: Object, default: null },
  kind: { type: String, default: 'http' },
  ruleLabel: { type: String, default: '' },
  endpointLabel: { type: String, default: '' },
  agentLabel: { type: String, default: '' }
})

defineEmits(['update:modelValue'])

const state = computed(() => props.task?.state || 'pending')
const busy = computed(() => !['completed', 'failed'].includes(state.value))
const summary = computed(() => props.task?.result?.summary || null)
const title = computed(() => props.kind === 'l4_tcp' ? 'L4 规则诊断' : 'HTTP 规则诊断')
const kindLabel = computed(() => props.kind === 'l4_tcp' ? 'TCP PATH DIAGNOSIS' : 'HTTP PATH DIAGNOSIS')
const stateLabel = computed(() => diagnosticStateLabel(state.value))
const tone = computed(() => diagnosticStateTone(state.value))
const agentLabel = computed(() => {
  const explicitLabel = props.agentLabel.trim()
  if (explicitLabel) return explicitLabel
  return props.task?.agent_id || ''
})
const backendSummaries = computed(() => props.task?.result?.backends || [])
const samples = computed(() => props.task?.result?.samples || [])
const relayPaths = computed(() => props.task?.result?.relay_paths || [])
const selectedRelayPath = computed(() => props.task?.result?.selected_relay_path || [])
const hasRelayPaths = computed(() => relayPaths.value.length > 0)
const pathStats = computed(() => {
  const paths = relayPaths.value
  if (!paths.length) return null
  const total = paths.length
  const success = paths.filter(p => p.success).length
  return { total, success, failed: total - success }
})

const isHTTP = computed(() => props.kind === 'http')
const showHTTPAdaptiveMetrics = computed(() => isHTTP.value)
const backendTableGridStyle = computed(() => ({
  gridTemplateColumns: isHTTP.value
    ? '1fr 70px 70px 70px 90px 70px'
    : '1fr 70px 70px 70px 70px'
}))

const showSamples = ref(false)
const showBackends = ref(false)
const expandedAdaptive = ref(new Set())

function toggleAdaptive(backendName) {
  const next = new Set(expandedAdaptive.value)
  if (next.has(backendName)) next.delete(backendName)
  else next.add(backendName)
  expandedAdaptive.value = next
}

function isAdaptiveExpanded(backendName) {
  return expandedAdaptive.value.has(backendName)
}

function backendActualLatency(backend) {
  const adaptiveLatency = Number(backend?.adaptive?.latency_ms)
  if (Number.isFinite(adaptiveLatency) && adaptiveLatency > 0) return adaptiveLatency
  const summaryLatency = Number(backend?.summary?.avg_latency_ms)
  if (Number.isFinite(summaryLatency)) return summaryLatency
  return 0
}

function displayAdaptive(backend) {
  const parentAdaptive = backend?.adaptive || null
  const preferredChild = (backend?.children || []).find(child => child?.adaptive?.preferred && adaptiveHasRecentSamples(child.adaptive))
  if (!preferredChild || adaptiveHasRecentSamples(parentAdaptive)) return parentAdaptive
  return mergeAdaptiveForDisplay(parentAdaptive, preferredChild.adaptive)
}

function adaptiveHasRecentSamples(adaptive) {
  return Number(adaptive?.recent_succeeded || 0) > 0 || Number(adaptive?.recent_failed || 0) > 0
}

function mergeAdaptiveForDisplay(parentAdaptive, childAdaptive) {
  if (!parentAdaptive) return childAdaptive || null
  if (!childAdaptive) return parentAdaptive
  return {
    ...parentAdaptive,
    ...childAdaptive,
    preferred: parentAdaptive.preferred,
    reason: parentAdaptive.reason || childAdaptive.reason,
    sustained_throughput_bps: childAdaptive.sustained_throughput_bps || parentAdaptive.sustained_throughput_bps
  }
}

function splitBackendIdentity(value) {
  const explicitAddress = typeof value?.address === 'string' ? value.address.trim() : ''
  const raw = typeof value === 'string' ? value.trim() : typeof value?.backend === 'string' ? value.backend.trim() : ''
  if (explicitAddress) {
    const match = raw.match(/^(.*)\s\[(.+)\]$/)
    return {
      label: match ? match[1].trim() : raw,
      address: explicitAddress
    }
  }
  if (!raw) return { label: '', address: '' }
  const match = raw.match(/^(.*)\s\[(.+)\]$/)
  if (!match) return { label: raw, address: '' }
  return {
    label: match[1].trim(),
    address: match[2].trim()
  }
}

function backendDisplayLabel(value) {
  return splitBackendIdentity(value).label
}

function backendDisplayAddress(value) {
  return splitBackendIdentity(value).address
}

function formatPathRoute(pathReport) {
  if (!pathReport.hops || pathReport.hops.length === 0) {
    return pathReport.path.map(id => '中继器 #' + id).join(' → ')
  }
  const labels = []
  for (const hop of pathReport.hops) {
    if (hop.to_listener_name) {
      const agentLabel = hop.to_agent_name ? ` (${hop.to_agent_name})` : ''
      labels.push(`${hop.to_listener_name}${agentLabel}`)
    } else if (hop.to_listener_id) {
      labels.push(`中继器 #${hop.to_listener_id}`)
    } else if (hop.to) {
      labels.push(`后端(${hop.to})`)
    }
  }
  return labels.join(' → ')
}

function formatHopLabel(hop) {
  if (hop.from === 'client') {
    if (hop.to_listener_name) {
      const agentLabel = formatRelayAgentLabel(hop.to_agent_name)
      return `入口 → ${hop.to_listener_name}${agentLabel}`
    }
    if (hop.to_listener_id) return `入口 → 中继器 #${hop.to_listener_id}`
    return `入口 → 后端(${hop.to || '-'})`
  }
  const fromAgent = formatRelayAgentLabel(hop.from_agent_name)
  const from = hop.from_listener_name
    ? `${hop.from_listener_name}${fromAgent}`
    : (hop.from_listener_id ? `中继器 #${hop.from_listener_id}` : (hop.from || '-'))
  if (hop.to_listener_name) {
    const toAgent = formatRelayAgentLabel(hop.to_agent_name)
    return `${from} → ${hop.to_listener_name}${toAgent}`
  }
  if (hop.to_listener_id) return `${from} → 中继器 #${hop.to_listener_id}`
  return `${from} → 后端(${hop.to || '-'})`
}

function formatRelayAgentLabel(value) {
  const label = typeof value === 'string' ? value.trim() : ''
  if (!label || isOpaqueAgentIdentifier(label)) return ''
  return ` (${label})`
}

function isOpaqueAgentIdentifier(value) {
  return /^[a-f0-9]{24,}$/i.test(value) || /^[a-f0-9-]{32,}$/i.test(value)
}

const QUALITY_MAP = {
  '极佳': 'success',
  '良好': 'info',
  '一般': 'warning',
  '较差': 'danger',
  '已连通': 'success',
  '未检测': 'muted',
  '不可用': 'danger'
}

function formatPercent(value) {
  if (value == null) return '-'
  return `${Math.round(Number(value) * 100)}%`
}

function formatThroughput(value) {
  if (value == null) return '-'
  const num = Number(value)
  if (!Number.isFinite(num) || num <= 0) return '-'
  if (num >= 1024 * 1024) return `${(num / (1024 * 1024)).toFixed(1)} MB/s`
  if (num >= 1024) return `${(num / 1024).toFixed(1)} KB/s`
  return `${num.toFixed(0)} B/s`
}

function formatScore(value) {
  if (value == null) return '-'
  return Number(value).toFixed(2)
}

function reasonLabel(value) {
  return {
    performance_higher: '综合性能更高',
    stability_higher: '近24h稳定性更高'
  }[value] || value || '-'
}

function trafficShareLabel(value) {
  return {
    normal: '主流量',
    cold: '冷启动探索',
    recovery: '恢复探索'
  }[value] || value || '-'
}

function outlierLabel(value) {
  if (value == null) return '-'
  return value ? '已降权' : '正常'
}

function httpStatusTone(code) {
  if (!code) return 'muted'
  if (code >= 200 && code < 300) return 'success'
  if (code >= 300 && code < 400) return 'info'
  if (code >= 400 && code < 500) return 'warning'
  if (code >= 500) return 'danger'
  return 'muted'
}

function classifyHopQuality(latencyMs) {
  if (latencyMs == null) return '不可用'
  if (latencyMs <= 50) return '极佳'
  if (latencyMs <= 150) return '良好'
  if (latencyMs <= 300) return '一般'
  return '较差'
}

function hopHasLatency(hop) {
  return Number.isFinite(Number(hop?.latency_ms))
}

function hopState(hop) {
  if (hop?.state) return hop.state
  return hop?.success ? 'success' : 'failed'
}

function hopIsSuccess(hop) {
  return hopState(hop) === 'success'
}

function hopIsFailed(hop) {
  return hopState(hop) === 'failed'
}

function hopStatusLabel(hop) {
  return {
    success: '成功',
    failed: '失败',
    untested: '未检测'
  }[hopState(hop)] || '未知'
}

function hopStatusTone(hop) {
  return {
    success: 'success',
    failed: 'danger',
    untested: 'muted'
  }[hopState(hop)] || 'muted'
}

function hopStatusIcon(hop) {
  return {
    success: '✓',
    failed: '✕',
    untested: '—'
  }[hopState(hop)] || '—'
}

function hopStatusIconClass(hop) {
  return `status-icon--${hopStatusTone(hop)}`
}

function hopLatencyClass(hop) {
  if (hopHasLatency(hop)) return 'value-primary'
  if (hopIsSuccess(hop)) return 'value-muted'
  if (hopIsFailed(hop)) return 'value-danger'
  return 'value-muted'
}

function hopQualityLabel(hop) {
  if (hopState(hop) === 'untested') return '未检测'
  if (!hopIsSuccess(hop)) return '不可用'
  if (!hopHasLatency(hop)) return '已连通'
  return classifyHopQuality(Number(hop.latency_ms))
}

function qualityLabelFor(value) {
  if (!value) return '-'
  return {
    excellent: '极佳',
    good: '良好',
    fair: '一般',
    poor: '较差',
    down: '不可用'
  }[value] || value
}

function qualityToneFor(value) {
  return QUALITY_MAP[qualityLabelFor(value)] || 'muted'
}
</script>

<style scoped>
.diagnostic-modal { display: flex; flex-direction: column; gap: 1rem; }

.diagnostic-modal__hero {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.1rem;
  border-radius: 16px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
}
.diagnostic-modal__hero-text { min-width: 0; }
.diagnostic-modal__eyebrow { font-size: 0.7rem; letter-spacing: 0.08em; color: var(--color-text-tertiary); font-weight: 600; }
.diagnostic-modal__headline { margin: 0.2rem 0 0.25rem; font-size: 1rem; font-weight: 700; color: var(--color-text-primary); line-height: 1.35; }
.diagnostic-modal__subtitle { margin: 0; font-family: var(--font-mono); font-size: 0.78rem; color: var(--color-text-secondary); word-break: break-all; line-height: 1.4; }
.diagnostic-modal__meta { margin: 0.3rem 0 0; font-size: 0.72rem; color: var(--color-text-tertiary); }
.diagnostic-modal__state {
  align-self: flex-start;
  padding: 0.25rem 0.65rem;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 700;
  border: 1px solid transparent;
}
.diagnostic-modal__state--success { background: rgba(16, 185, 129, 0.12); color: var(--color-success); border-color: rgba(16, 185, 129, 0.25); box-shadow: 0 0 10px rgba(16, 185, 129, 0.12); }
.diagnostic-modal__state--danger { background: rgba(239, 68, 68, 0.12); color: var(--color-danger); border-color: rgba(239, 68, 68, 0.25); box-shadow: 0 0 10px rgba(239, 68, 68, 0.12); }
.diagnostic-modal__state--info { background: rgba(56, 189, 248, 0.12); color: var(--color-primary); border-color: rgba(56, 189, 248, 0.25); box-shadow: 0 0 10px rgba(56, 189, 248, 0.12); }
.diagnostic-modal__state--muted { background: var(--color-bg-hover); color: var(--color-text-muted); border-color: var(--color-border-subtle); }

.diagnostic-modal__loading, .diagnostic-modal__error {
  display: flex;
  align-items: center;
  gap: 0.875rem;
  padding: 0.875rem 1rem;
  border-radius: 14px;
  background: var(--color-bg-hover);
  border: 1px solid var(--color-border-subtle);
}
.diagnostic-modal__error { color: var(--color-danger); border-color: rgba(239, 68, 68, 0.25); }
.diagnostic-modal__pulse {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--color-primary);
  box-shadow: 0 0 12px rgba(13, 148, 136, 0.4);
  animation: diag-pulse 1.5s infinite;
}
.diagnostic-modal__loading-title { font-weight: 700; color: var(--color-text-primary); }
.diagnostic-modal__loading-text { font-size: 0.82rem; color: var(--color-text-secondary); }

.diagnostic-modal__stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.75rem;
}
.diagnostic-stat {
  padding: 0.875rem 1rem;
  border-radius: 14px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  text-align: center;
}
.diagnostic-stat--success { background: var(--color-success-50); border-color: var(--color-success); }
.diagnostic-stat--success .diagnostic-stat__value { color: var(--color-success); }
.diagnostic-stat--danger { background: var(--color-danger-50); border-color: var(--color-danger); }
.diagnostic-stat--danger .diagnostic-stat__value { color: var(--color-danger); }
.diagnostic-stat__label { font-size: 0.68rem; color: var(--color-primary); font-weight: 500; }
.diagnostic-stat__value { font-size: 0.95rem; color: var(--color-text-primary); font-weight: 700; }

.diagnostic-section { display: flex; flex-direction: column; gap: 0.75rem; }
.diagnostic-section__title {
  font-size: 0.85rem;
  font-weight: 700;
  color: var(--color-text-primary);
  padding-left: 0.25rem;
}

.relay-path-card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: 12px;
  overflow: hidden;
}
.relay-path-card__header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.6rem 0.75rem;
  border-bottom: 1px solid var(--color-border-subtle);
  font-size: 0.82rem;
}
.relay-path-card__name { font-weight: 700; color: var(--color-text-primary); }
.relay-path-card__route { color: var(--color-text-secondary); font-family: var(--font-mono); }
.relay-path-card__latency { margin-left: auto; font-size: 0.75rem; color: var(--color-primary); font-weight: 600; }
.relay-path-card__error {
  padding: 0.5rem 0.75rem;
  font-size: 0.78rem;
  color: var(--color-danger);
}

.diagnostic-table {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: 12px;
  overflow: hidden;
}
.diagnostic-table__header {
  display: grid;
  grid-template-columns: 1fr 80px 80px 80px;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  font-size: 0.7rem;
  color: var(--color-text-tertiary);
  font-weight: 600;
  border-bottom: 1px solid var(--color-border-default);
}
.diagnostic-table__row {
  display: grid;
  grid-template-columns: 1fr 80px 80px 80px;
  gap: 0.5rem;
  padding: 0.55rem 0.75rem;
  align-items: center;
  border-bottom: 1px solid var(--color-border-subtle);
}
.diagnostic-table__row:last-child { border-bottom: none; }
.diagnostic-table__row--failed { background: var(--color-danger-50); }
.diagnostic-table__cell {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.82rem;
  min-width: 0;
}
.status-icon--success { color: var(--color-success); }
.status-icon--danger { color: var(--color-danger); }
.status-icon--muted { color: var(--color-text-muted); }
.value-primary { color: var(--color-primary); font-weight: 600; }
.value-danger { color: var(--color-danger); font-weight: 600; }
.value-muted { color: var(--color-text-muted); }

.pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: var(--radius-full);
  font-size: 0.65rem;
  font-weight: 700;
}
.pill--success { background: var(--color-success-50); color: var(--color-success); border: 1px solid var(--color-success); }
.pill--danger { background: var(--color-danger-50); color: var(--color-danger); border: 1px solid var(--color-danger); }
.pill--warning { background: var(--color-warning-50); color: var(--color-warning); border: 1px solid var(--color-warning); }
.pill--info { background: var(--color-primary-subtle); color: var(--color-primary); border: 1px solid var(--color-primary); }
.pill--preferred { background: rgba(245, 158, 11, 0.12); color: #d97706; border: 1px solid rgba(245, 158, 11, 0.35); font-size: 0.6rem; padding: 1px 6px; }
.pill--muted { background: var(--color-bg-hover); color: var(--color-text-muted); border: 1px solid var(--color-border-subtle); }

.slide-expand-enter-active,
.slide-expand-leave-active {
  transition: max-height 0.3s ease, opacity 0.25s ease;
  overflow: hidden;
}
.slide-expand-enter-from,
.slide-expand-leave-to {
  max-height: 0;
  opacity: 0;
}
.slide-expand-enter-to,
.slide-expand-leave-from {
  max-height: 400px;
  opacity: 1;
}

.diagnostic-modal__backends {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.diagnostic-backend-list {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.diagnostic-backend-item {
  padding: 0.85rem 1rem;
  border-radius: 12px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.diagnostic-backend-item__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.75rem;
}
.diagnostic-backend-item__name {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--color-text-primary);
  word-break: break-all;
}
.diagnostic-backend-item__badges {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.3rem;
}
.diagnostic-backend-item__preferred {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 0.62rem;
  font-weight: 700;
  background: rgba(16, 185, 129, 0.12);
  color: var(--color-success);
  border: 1px solid rgba(16, 185, 129, 0.25);
}
.diagnostic-backend-item__quality {
  flex-shrink: 0;
  font-size: 0.62rem;
  font-weight: 700;
  padding: 2px 10px;
  border-radius: 999px;
  background: var(--color-bg-hover);
  color: var(--color-text-muted);
}
.diagnostic-backend-item__quality--success { background: var(--color-success-50); color: var(--color-success); }
.diagnostic-backend-item__quality--info { background: var(--color-primary-subtle); color: var(--color-primary); }
.diagnostic-backend-item__quality--warning { background: var(--color-warning-50); color: var(--color-warning); }
.diagnostic-backend-item__quality--danger { background: var(--color-danger-50); color: var(--color-danger); }

.diagnostic-backend-item__metrics {
  display: flex;
  gap: 0.4rem;
}
.diagnostic-metric {
  flex: 1 1 0;
  min-width: 0;
  padding: 0.4rem 0.5rem;
  border-radius: 10px;
  background: var(--color-primary-subtle);
  border: 1px solid var(--color-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.05rem;
}
.diagnostic-metric__label {
  font-size: 0.6rem;
  color: var(--color-primary);
  font-weight: 500;
}
.diagnostic-metric__value {
  font-size: 0.84rem;
  color: var(--color-text-primary);
  font-weight: 700;
}
.diagnostic-backend-item__probe {
  display: flex;
  align-items: center;
  gap: 1rem;
  font-size: 0.72rem;
  color: var(--color-text-secondary);
}
.diagnostic-backend-item__probe-stat strong {
  color: var(--color-primary);
  font-weight: 600;
}
.diagnostic-backend-item__toggle {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.22rem 0.6rem;
  border-radius: 8px;
  border: 1px solid var(--color-border-subtle);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-size: 0.72rem;
  cursor: pointer;
}
.diagnostic-backend-item__toggle:hover {
  background: var(--color-bg-hover);
}
.diagnostic-backend-item__toggle-icon {
  font-size: 0.7rem;
  color: var(--color-primary);
  transition: transform 0.25s ease;
}
.diagnostic-backend-item__toggle-icon--open {
  transform: rotate(90deg);
}
.diagnostic-backend-item__details {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.diagnostic-backend-item__details-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.4rem;
}
@media (max-width: 520px) {
  .diagnostic-backend-item__details-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
.diagnostic-backend-item__reason {
  font-size: 0.72rem;
  color: var(--color-text-secondary);
}
.diagnostic-backend-item__children {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  padding-top: 0.4rem;
  border-top: 1px solid var(--color-border-subtle);
}
.diagnostic-backend-item__child-title {
  font-size: 0.68rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
  margin-bottom: 0.15rem;
}
.diagnostic-child-list {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.diagnostic-child-item {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.4rem;
  padding: 0.25rem 0;
  border-bottom: 1px solid var(--color-border-subtle);
}
.diagnostic-child-item:last-child {
  border-bottom: none;
}
.diagnostic-child-item__name,
.diagnostic-child-item__address {
  font-family: var(--font-mono);
  word-break: break-all;
}
.diagnostic-child-item__name {
  font-size: 0.72rem;
  color: var(--color-text-primary);
}
.diagnostic-child-item__address {
  font-size: 0.7rem;
  color: var(--color-text-tertiary);
}
.diagnostic-child-item__metric {
  font-size: 0.68rem;
  color: var(--color-text-secondary);
  white-space: nowrap;
}
.diagnostic-factor {
  padding: 0.4rem 0.5rem;
  border-radius: 10px;
  background: var(--color-primary-subtle);
  border: 1px solid var(--color-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}
.diagnostic-factor__label {
  font-size: 0.6rem;
  color: var(--color-primary);
  font-weight: 500;
}
.diagnostic-factor__value {
  font-size: 0.82rem;
  color: var(--color-text-primary);
}
.diagnostic-modal__section-title {
  font-weight: 700;
  color: var(--color-text-primary);
  margin-bottom: 0.35rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.85rem;
}
.diagnostic-modal__section-title--toggle {
  width: 100%;
  background: transparent;
  border: none;
  padding: 0.3rem 0;
  cursor: pointer;
  font: inherit;
  text-align: left;
  border-radius: 8px;
}
.diagnostic-modal__section-title--toggle:hover {
  background: var(--color-bg-hover);
}
.diagnostic-modal__toggle {
  font-size: 0.85rem;
  color: var(--color-text-tertiary);
  transition: transform 0.2s ease;
  margin-left: auto;
}
.diagnostic-modal__toggle--open {
  transform: rotate(90deg);
}
.diagnostic-modal__sample-count {
  font-size: 0.68rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
  background: var(--color-bg-hover);
  padding: 2px 7px;
  border-radius: 999px;
}
.diagnostic-modal__samples {
  display: flex;
  flex-direction: column;
}
.diagnostic-sample-list {
  max-height: 220px;
  overflow-y: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: 12px;
  padding: 0.3rem 0.4rem;
  background: var(--color-bg-surface);
}
.diagnostic-sample {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.4rem 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.diagnostic-sample:last-child {
  border-bottom: none;
}
.diagnostic-sample--failed {
  color: var(--color-danger);
  background: rgba(239, 68, 68, 0.04);
}
.diagnostic-sample__left {
  display: flex;
  gap: 0.55rem;
  min-width: 0;
  align-items: flex-start;
}
.diagnostic-sample__attempt {
  font-size: 0.72rem;
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
  min-width: 2.2ch;
  text-align: right;
}
.diagnostic-sample__status {
  font-size: 0.65rem;
  font-weight: 700;
  padding: 1px 4px;
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
}
.diagnostic-sample__status--success { background: var(--color-success-50); color: var(--color-success); }
.diagnostic-sample__status--info { background: rgba(56, 189, 248, 0.12); color: #0ea5e9; }
.diagnostic-sample__status--warning { background: var(--color-warning-50); color: var(--color-warning); }
.diagnostic-sample__status--danger { background: var(--color-danger-50); color: var(--color-danger); }
.diagnostic-sample__status--muted { background: var(--color-bg-hover); color: var(--color-text-muted); }
.diagnostic-sample__backend-wrap {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}
.diagnostic-sample__backend,
.diagnostic-sample__backend-address {
  font-family: var(--font-mono);
  word-break: break-all;
}
.diagnostic-sample__backend {
  font-size: 0.78rem;
  color: var(--color-text-secondary);
}
.diagnostic-sample__backend-address {
  font-size: 0.72rem;
  color: var(--color-text-tertiary);
}
.diagnostic-sample__right {
  font-size: 0.78rem;
  color: var(--color-text-secondary);
  white-space: nowrap;
  font-family: var(--font-mono);
}
.diagnostic-sample--failed .diagnostic-sample__right {
  font-weight: 500;
}

.truncate {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@keyframes diag-pulse {
  0% { box-shadow: 0 0 0 0 rgba(13, 148, 136, 0.4); }
  70% { box-shadow: 0 0 14px 8px rgba(13, 148, 136, 0); }
  100% { box-shadow: 0 0 0 0 rgba(13, 148, 136, 0); }
}
</style>
