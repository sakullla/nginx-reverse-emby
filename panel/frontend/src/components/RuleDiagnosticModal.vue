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

        <div class="diagnostic-modal__range">
          <div class="latency-bar">
            <div class="latency-bar__track">
              <div
                class="latency-bar__fill"
              ></div>
            </div>
            <div class="latency-bar__labels">
              <span>最小 {{ summary.min_latency_ms ?? 0 }} ms</span>
              <span>最大 {{ summary.max_latency_ms ?? 0 }} ms</span>
            </div>
          </div>
        </div>

      </template>
    </div>
  </BaseModal>
</template>

<script setup>
import { computed } from 'vue'
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
const backendSummaries = computed(() => props.task?.result?.backends || [])
const title = computed(() => props.kind === 'l4_tcp' ? 'L4 规则诊断' : 'HTTP 规则诊断')
const kindLabel = computed(() => props.kind === 'l4_tcp' ? 'TCP PATH DIAGNOSIS' : 'HTTP PATH DIAGNOSIS')
const stateLabel = computed(() => diagnosticStateLabel(state.value))
const tone = computed(() => diagnosticStateTone(state.value))
const agentLabel = computed(() => props.task?.agent_id || '')
const isHTTP = computed(() => props.kind === 'http')
const showHTTPAdaptiveMetrics = computed(() => isHTTP.value)
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

function adaptiveStateLabel(value) {
  return {
    cold: '冷启动',
    recovering: '恢复中',
    warm: '稳定'
  }[value] || value || '-'
}

function trafficShareLabel(value) {
  return {
    normal: '主流量',
    cold: '冷启动探索',
    recovery: '恢复探索'
  }[value] || value || '-'
}

function slowStartLabel(value) {
  if (value == null) return '-'
  return value ? '进行中' : '无'
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
.diagnostic-stat__value--caps { text-transform: uppercase; letter-spacing: 0.06em; font-size: 0.88rem; }
.diagnostic-stat__value--success { color: var(--color-success); }
.diagnostic-stat__value--info { color: var(--color-primary); }
.diagnostic-stat__value--warning { color: var(--color-warning); }
.diagnostic-stat__value--danger { color: var(--color-danger); }
.diagnostic-stat__value--muted { color: var(--color-text-muted); }

/* ── Latency Bar ── */
.diagnostic-modal__range {
  padding: 0 0.1rem;
}
.latency-bar { display: flex; flex-direction: column; gap: 0.35rem; }
.latency-bar__track {
  height: 4px;
  border-radius: 999px;
  background: var(--color-bg-hover);
  overflow: hidden;
}
.latency-bar__fill {
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(90deg, var(--color-success), var(--color-primary));
  box-shadow: 0 0 8px var(--color-primary-subtle);
  transition: width 0.4s ease;
}
.latency-bar__labels {
  display: flex;
  justify-content: space-between;
  font-size: 0.78rem;
  color: var(--color-text-tertiary);
}

/* ── Backend Cards ── */
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
  border-radius: 16px;
  background: linear-gradient(135deg, var(--color-primary-subtle) 0%, rgba(0,0,0,0) 100%);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  border: 1px solid var(--color-border-subtle);
  box-shadow: 0 2px 16px rgba(0,0,0,0.08), inset 0 1px 0 rgba(255,255,255,0.04);
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  position: relative;
  overflow: hidden;
}
.diagnostic-backend-item::after {
  content: '';
  position: absolute;
  top: -20px;
  right: -20px;
  width: 80px;
  height: 80px;
  background: radial-gradient(circle, var(--color-primary-subtle) 0%, transparent 70%);
  opacity: 0.6;
  pointer-events: none;
}
.diagnostic-backend-item__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.75rem;
  position: relative;
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
  background: linear-gradient(135deg, rgba(16, 185, 129, 0.15), rgba(16, 185, 129, 0.08));
  color: var(--color-success);
  border: 1px solid rgba(16, 185, 129, 0.25);
  box-shadow: 0 0 8px rgba(16, 185, 129, 0.1);
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
.diagnostic-backend-item__quality--success { background: linear-gradient(135deg, rgba(16,185,129,0.14), rgba(16,185,129,0.07)); color: var(--color-success); box-shadow: 0 0 8px rgba(16,185,129,0.08); }
.diagnostic-backend-item__quality--info { background: linear-gradient(135deg, rgba(56,189,248,0.14), rgba(56,189,248,0.07)); color: var(--color-primary); box-shadow: 0 0 8px rgba(56,189,248,0.08); }
.diagnostic-backend-item__quality--warning { background: linear-gradient(135deg, rgba(217,119,6,0.14), rgba(217,119,6,0.07)); color: var(--color-warning); box-shadow: 0 0 8px rgba(217,119,6,0.08); }
.diagnostic-backend-item__quality--danger { background: linear-gradient(135deg, rgba(239,68,68,0.14), rgba(239,68,68,0.07)); color: var(--color-danger); box-shadow: 0 0 8px rgba(239,68,68,0.08); }

/* ── Metrics Grid ── */
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
  box-shadow: inset 0 1px 0 rgba(255,255,255,0.03);
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

/* ── Probe Stats ── */
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

/* ── Toggle Button ── */
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
  transition: background 0.15s ease, box-shadow 0.15s ease;
}
.diagnostic-backend-item__toggle:hover {
  background: var(--color-bg-hover);
  box-shadow: 0 0 6px var(--color-primary-subtle);
}
.diagnostic-backend-item__toggle-icon {
  font-size: 0.7rem;
  color: var(--color-primary);
  transition: transform 0.25s ease;
}
.diagnostic-backend-item__toggle-icon--open {
  transform: rotate(90deg);
}

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

/* ── Details (expanded) ── */
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

/* ── Children ── */
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
.diagnostic-child-item__name {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--color-text-primary);
  word-break: break-all;
}
.diagnostic-child-item__address {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--color-text-tertiary);
  word-break: break-all;
}
.diagnostic-child-item__metric {
  font-size: 0.68rem;
  color: var(--color-text-secondary);
  white-space: nowrap;
}

/* ── Factor (in expanded details) ── */
.diagnostic-factor {
  padding: 0.4rem 0.5rem;
  border-radius: 10px;
  background: var(--color-primary-subtle);
  border: 1px solid var(--color-border-subtle);
  box-shadow: inset 0 1px 0 rgba(255,255,255,0.03);
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

/* ── Section Titles ── */
.diagnostic-modal__section-title { font-weight: 700; color: var(--color-text-primary); margin-bottom: 0.35rem; display: flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; }
.diagnostic-modal__section-title--toggle {
  width: 100%;
  background: transparent;
  border: none;
  padding: 0.3rem 0;
  cursor: pointer;
  font: inherit;
  text-align: left;
  border-radius: 8px;
  transition: background 0.15s ease;
}
.diagnostic-modal__section-title--toggle:hover {
  background: var(--color-bg-hover);
}
.diagnostic-modal__toggle { font-size: 0.85rem; color: var(--color-text-tertiary); transition: transform 0.2s ease; margin-left: auto; }
.diagnostic-modal__toggle--open { transform: rotate(90deg); }
.diagnostic-modal__sample-count { font-size: 0.68rem; font-weight: 600; color: var(--color-text-tertiary); background: var(--color-bg-hover); padding: 2px 7px; border-radius: 999px; }

/* ── Samples ── */
.diagnostic-modal__samples { display: flex; flex-direction: column; }
.diagnostic-sample-list {
  max-height: 220px;
  overflow-y: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: 14px;
  padding: 0.3rem 0.4rem;
  background: var(--color-bg-surface);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
  box-shadow: inset 0 1px 0 rgba(255,255,255,0.03);
}
.diagnostic-sample-list::-webkit-scrollbar { width: 6px; }
.diagnostic-sample-list::-webkit-scrollbar-thumb { background: var(--color-border-default); border-radius: 3px; }
.diagnostic-sample {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.4rem 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
  transition: background 0.12s ease;
}
.diagnostic-sample:last-child {
  border-bottom: none;
}
.diagnostic-sample + .diagnostic-sample {
  margin-top: 0;
}
.diagnostic-sample:hover { background: var(--color-bg-hover); }
.diagnostic-sample--failed { color: var(--color-danger); background: rgba(239, 68, 68, 0.04); }
.diagnostic-sample--failed:hover { background: rgba(239, 68, 68, 0.08); }
.diagnostic-sample__left {
  display: flex;
  gap: 0.55rem;
  min-width: 0;
  align-items: flex-start;
}
.diagnostic-sample__attempt { font-size: 0.72rem; color: var(--color-text-tertiary); font-family: var(--font-mono); min-width: 2.2ch; text-align: right; }
.diagnostic-sample__status { font-size: 0.65rem; font-weight: 700; padding: 1px 4px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
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
.diagnostic-sample__backend {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--color-text-secondary);
  word-break: break-all;
}
.diagnostic-sample__backend-address {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--color-text-tertiary);
  word-break: break-all;
}
.diagnostic-sample__right {
  font-size: 0.78rem;
  color: var(--color-text-secondary);
  white-space: nowrap;
  font-family: var(--font-mono);
}
.diagnostic-sample--failed .diagnostic-sample__right { font-weight: 500; }

/* ── Pulse Animation ── */
@keyframes diag-pulse {
  0% { box-shadow: 0 0 0 0 rgba(13, 148, 136, 0.4); }
  70% { box-shadow: 0 0 14px 8px rgba(13, 148, 136, 0); }
  100% { box-shadow: 0 0 0 0 rgba(13, 148, 136, 0); }
}
</style>
