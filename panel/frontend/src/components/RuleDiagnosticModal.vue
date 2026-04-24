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

        <div v-if="hasRelayPaths" class="diagnostic-modal__relay-paths">
          <div v-for="(pathReport, pathIndex) in relayPaths" :key="pathIndex" class="relay-path-section">
            <div class="relay-path-section__header">
              <span class="relay-path-section__icon">🔄</span>
              <span class="relay-path-section__title">路径 {{ pathIndex + 1 }} ({{ pathReport.path.map(id => '#' + id).join(' → ') }})</span>
              <span v-if="isSelectedPath(pathReport.path)" class="pill pill--success">已选中</span>
              <span :class="`pill pill--${pathReport.success ? 'success' : 'danger'}`">
                {{ pathReport.success ? '成功' : '失败' }}
              </span>
            </div>

            <div class="relay-path-section__table-wrap">
              <div class="diagnostic-table">
                <div class="diagnostic-table__header">
                  <span>路径</span>
                  <span style="text-align:center">状态</span>
                  <span style="text-align:center">延迟</span>
                  <span style="text-align:center">丢包率</span>
                  <span style="text-align:center">质量</span>
                </div>
                <div
                  v-for="(hop, hopIndex) in pathReport.hops"
                  :key="hopIndex"
                  class="diagnostic-table__row"
                  :class="{ 'diagnostic-table__row--failed': !hop.success }"
                >
                  <div class="diagnostic-table__cell">
                    <span :class="hop.success ? 'status-icon--success' : 'status-icon--danger'">
                      {{ hop.success ? '✓' : '✕' }}
                    </span>
                    <span>{{ formatHopPath(hop) }} → {{ formatHopTarget(hop) }}</span>
                  </div>
                  <span class="diagnostic-table__cell" style="text-align:center">
                    <span :class="`pill pill--${hop.success ? 'success' : 'danger'}`">
                      {{ hop.success ? '成功' : '失败' }}
                    </span>
                  </span>
                  <span class="diagnostic-table__cell" style="text-align:center">
                    <span :class="hop.success ? 'value-primary' : 'value-danger'">
                      {{ hop.success ? (hop.latency_ms + ' ms') : '—' }}
                    </span>
                  </span>
                  <span class="diagnostic-table__cell" style="text-align:center">—</span>
                  <span class="diagnostic-table__cell" style="text-align:center">
                    <span :class="`pill pill--${qualityToneFor(classifyHopQuality(hop.latency_ms))}`">
                      {{ hop.success ? classifyHopQuality(hop.latency_ms) : '不可用' }}
                    </span>
                  </span>
                </div>
              </div>
            </div>

            <div v-if="pathReport.error" class="relay-path-section__error">
              错误: {{ pathReport.error }}
            </div>
          </div>
        </div>

        <div class="diagnostic-modal__backend-section">
          <button
            type="button"
            class="diagnostic-modal__section-toggle"
            @click="showBackends = !showBackends"
          >
            <span>{{ showBackends ? '▾' : '▸' }}</span>
            <span>后端探测结果</span>
            <span v-if="backendSummaries.length" class="count-badge">{{ backendSummaries.length }} 个</span>
          </button>

          <Transition name="slide-expand">
            <div v-if="showBackends" class="diagnostic-table-wrap">
              <div class="diagnostic-table">
                <div class="diagnostic-table__header" :style="backendTableGridStyle">
                  <span>路径</span>
                  <span style="text-align:center">状态</span>
                  <span style="text-align:center">延迟</span>
                  <span style="text-align:center">丢包率</span>
                  <span v-if="isHTTP" style="text-align:center">持续吞吐</span>
                  <span style="text-align:center">质量</span>
                </div>
                <div
                  v-for="backend in backendSummaries"
                  :key="backend.backend"
                  class="diagnostic-table__row"
                  :class="{ 'diagnostic-table__row--failed': backend.summary?.succeeded !== backend.summary?.sent }"
                  :style="backendTableGridStyle"
                >
                  <div class="diagnostic-table__cell">
                    <span :class="(backend.summary?.succeeded === backend.summary?.sent) ? 'status-icon--success' : 'status-icon--danger'">
                      {{ (backend.summary?.succeeded === backend.summary?.sent) ? '✓' : '✕' }}
                    </span>
                    <span class="truncate">{{ ruleLabel }} → {{ backendDisplayLabel(backend) }}{{ backendDisplayAddress(backend) ? ' [' + backendDisplayAddress(backend) + ']' : '' }}</span>
                  </div>
                  <span class="diagnostic-table__cell" style="text-align:center">
                    <span :class="`pill pill--${(backend.summary?.succeeded === backend.summary?.sent) ? 'success' : 'danger'}`">
                      {{ (backend.summary?.succeeded === backend.summary?.sent) ? '成功' : '失败' }}
                    </span>
                  </span>
                  <span class="diagnostic-table__cell" style="text-align:center">
                    <span class="value-primary">{{ backend.summary?.avg_latency_ms ?? 0 }} ms</span>
                  </span>
                  <span class="diagnostic-table__cell" style="text-align:center">{{ formatPercent(backend.summary?.loss_rate) }}</span>
                  <span v-if="isHTTP" class="diagnostic-table__cell" style="text-align:center">
                    <span class="value-primary">{{ formatThroughput(backend.adaptive?.sustained_throughput_bps) }}</span>
                  </span>
                  <span class="diagnostic-table__cell" style="text-align:center">
                    <span :class="`pill pill--${qualityToneFor(backend.summary?.quality)}`">
                      {{ qualityLabelFor(backend.summary?.quality) }}
                    </span>
                  </span>
                </div>
              </div>
            </div>
          </Transition>
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
  endpointLabel: { type: String, default: '' }
})

defineEmits(['update:modelValue'])

const state = computed(() => props.task?.state || 'pending')
const busy = computed(() => !['completed', 'failed'].includes(state.value))
const summary = computed(() => props.task?.result?.summary || null)
const title = computed(() => props.kind === 'l4_tcp' ? 'L4 规则诊断' : 'HTTP 规则诊断')
const kindLabel = computed(() => props.kind === 'l4_tcp' ? 'TCP PATH DIAGNOSIS' : 'HTTP PATH DIAGNOSIS')
const stateLabel = computed(() => diagnosticStateLabel(state.value))
const tone = computed(() => diagnosticStateTone(state.value))
const agentLabel = computed(() => props.task?.agent_id || '')
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

const showBackends = ref(false)
const isHTTP = computed(() => props.kind === 'http')
const backendSummaries = computed(() => props.task?.result?.backends || [])
const backendTableGridStyle = computed(() => ({
  gridTemplateColumns: isHTTP.value
    ? '1fr 70px 70px 70px 90px 70px'
    : '1fr 70px 70px 70px 70px'
}))

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

const QUALITY_MAP = {
  '极佳': 'success',
  '良好': 'info',
  '一般': 'warning',
  '较差': 'danger',
  '不可用': 'danger'
}

const qualityLabel = computed(() => {
  const q = summary.value?.quality
  if (!q) return '-'
  // Support both Chinese (new) and English (legacy data) from backend
  const cn = {
    excellent: '极佳',
    good: '良好',
    fair: '一般',
    poor: '较差',
    down: '不可用'
  }[q]
  return cn || q || '-'
})

const qualityTone = computed(() => {
  return QUALITY_MAP[qualityLabel.value] || 'muted'
})

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

function formatHopPath(hop) {
  if (hop.from === 'client') return '入口'
  if (hop.from_listener_id) return `#${hop.from_listener_id}`
  return hop.from || '-'
}

function formatHopTarget(hop) {
  if (!hop.to_listener_id) return `出口(${hop.to || '-'})`
  return `#${hop.to_listener_id}`
}

function isSelectedPath(path) {
  if (!selectedRelayPath.value.length) return false
  if (path.length !== selectedRelayPath.value.length) return false
  return path.every((id, i) => id === selectedRelayPath.value[i])
}

function classifyHopQuality(latencyMs) {
  if (latencyMs == null) return '不可用'
  if (latencyMs <= 50) return '极佳'
  if (latencyMs <= 150) return '良好'
  if (latencyMs <= 300) return '一般'
  return '较差'
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
/* ── Layout ── */
.diagnostic-modal { display: flex; flex-direction: column; gap: 1rem; }

/* ── Hero ── */
.diagnostic-modal__hero {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.1rem;
  border-radius: 16px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
}
.diagnostic-modal__hero-text { min-width: 0; position: relative; }
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
  position: relative;
}
.diagnostic-modal__state--success { background: rgba(16, 185, 129, 0.12); color: var(--color-success); border-color: rgba(16, 185, 129, 0.25); box-shadow: 0 0 10px rgba(16, 185, 129, 0.12); }
.diagnostic-modal__state--danger { background: rgba(239, 68, 68, 0.12); color: var(--color-danger); border-color: rgba(239, 68, 68, 0.25); box-shadow: 0 0 10px rgba(239, 68, 68, 0.12); }
.diagnostic-modal__state--info { background: rgba(56, 189, 248, 0.12); color: var(--color-primary); border-color: rgba(56, 189, 248, 0.25); box-shadow: 0 0 10px rgba(56, 189, 248, 0.12); }
.diagnostic-modal__state--muted { background: var(--color-bg-hover); color: var(--color-text-muted); border-color: var(--color-border-subtle); }

/* ── Loading / Error ── */
.diagnostic-modal__loading, .diagnostic-modal__error {
  display: flex;
  align-items: center;
  gap: 0.875rem;
  padding: 0.875rem 1rem;
  border-radius: 14px;
  background: var(--color-bg-hover);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
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

/* ── Stats Grid ── */
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
.diagnostic-stat--success {
  background: var(--color-success-50);
  border-color: var(--color-success);
}
.diagnostic-stat--success .diagnostic-stat__value {
  color: var(--color-success);
}
.diagnostic-stat--danger {
  background: var(--color-danger-50);
  border-color: var(--color-danger);
}
.diagnostic-stat--danger .diagnostic-stat__value {
  color: var(--color-danger);
}
.diagnostic-stat__label { font-size: 0.68rem; color: var(--color-primary); font-weight: 500; }
.diagnostic-stat__value { font-size: 0.95rem; color: var(--color-text-primary); font-weight: 700; }

/* ── Expand/Collapse Animation ── */
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

/* ── Generic Pill ── */
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

/* ── Generic Table ── */
.diagnostic-table {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: 12px;
  overflow: hidden;
}
.diagnostic-table__header {
  display: grid;
  grid-template-columns: 1fr 80px 80px 80px 80px;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  font-size: 0.7rem;
  color: var(--color-text-tertiary);
  font-weight: 600;
  border-bottom: 1px solid var(--color-border-default);
}
.diagnostic-table__row {
  display: grid;
  grid-template-columns: 1fr 80px 80px 80px 80px;
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
.value-primary { color: var(--color-primary); font-weight: 600; }
.value-danger { color: var(--color-danger); font-weight: 600; }

/* ── Relay Path Section ── */
.relay-path-section { margin-bottom: 1rem; }
.relay-path-section__header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
  font-weight: 600;
  font-size: 0.85rem;
}
.relay-path-section__icon { font-size: 1rem; }
.relay-path-section__title { color: var(--color-text-primary); }
.relay-path-section__error {
  margin-top: 0.4rem;
  font-size: 0.78rem;
  color: var(--color-danger);
  padding-left: 0.5rem;
}

/* ── Pulse Animation ── */
@keyframes diag-pulse {
  0% { box-shadow: 0 0 0 0 rgba(13, 148, 136, 0.4); }
  70% { box-shadow: 0 0 14px 8px rgba(13, 148, 136, 0); }
  100% { box-shadow: 0 0 0 0 rgba(13, 148, 136, 0); }
}

/* ── Backend Section Toggle ── */
.diagnostic-modal__backend-section { margin-top: 0.5rem; }
.diagnostic-modal__section-toggle {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  background: transparent;
  border: none;
  padding: 0.5rem 0;
  color: var(--color-primary);
  font: inherit;
  font-weight: 600;
  font-size: 0.85rem;
  cursor: pointer;
  width: 100%;
  text-align: left;
}
.diagnostic-modal__section-toggle:hover {
  background: var(--color-bg-hover);
  border-radius: 8px;
}
.count-badge {
  font-size: 0.68rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
  background: var(--color-bg-hover);
  padding: 2px 7px;
  border-radius: var(--radius-full);
}
.truncate {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
