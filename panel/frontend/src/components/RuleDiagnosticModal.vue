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
            <span class="diagnostic-stat__label">平均延迟</span>
            <strong class="diagnostic-stat__value">{{ summary.avg_latency_ms ?? 0 }} ms</strong>
          </div>
          <div class="diagnostic-stat">
            <span class="diagnostic-stat__label">丢包率</span>
            <strong class="diagnostic-stat__value">{{ formatPercent(summary.loss_rate) }}</strong>
          </div>
          <div class="diagnostic-stat">
            <span class="diagnostic-stat__label">成功 / 总数</span>
            <strong class="diagnostic-stat__value">{{ summary.succeeded ?? 0 }} / {{ summary.sent ?? 0 }}</strong>
          </div>
          <div class="diagnostic-stat">
            <span class="diagnostic-stat__label">链路质量</span>
            <strong class="diagnostic-stat__value diagnostic-stat__value--caps" :class="`diagnostic-stat__value--${qualityTone}`">{{ qualityLabel }}</strong>
          </div>
        </div>

        <div class="diagnostic-modal__range">
          <div class="latency-bar">
            <div class="latency-bar__track">
              <div
                class="latency-bar__fill"
                :style="{ width: latencyBarPct + '%' }"
              ></div>
            </div>
            <div class="latency-bar__labels">
              <span>最小 {{ summary.min_latency_ms ?? 0 }} ms</span>
              <span>最大 {{ summary.max_latency_ms ?? 0 }} ms</span>
            </div>
          </div>
        </div>

        <div v-if="backendSummaries.length" class="diagnostic-modal__backends">
          <div class="diagnostic-modal__section-title">后端延迟</div>
          <div class="diagnostic-backend-list">
            <article v-for="backend in backendSummaries" :key="backend.backend" class="diagnostic-backend-item">
              <div class="diagnostic-backend-item__header">
                <code class="diagnostic-backend-item__name">{{ backend.backend }}</code>
                <div class="diagnostic-backend-item__badges">
                  <span v-if="backend.adaptive?.preferred" class="diagnostic-backend-item__preferred">当前优选</span>
                  <span class="diagnostic-backend-item__quality" :class="`diagnostic-backend-item__quality--${qualityToneFor(backend.summary?.quality)}`">
                    {{ qualityLabelFor(backend.summary?.quality) }}
                  </span>
                </div>
              </div>

              <div class="diagnostic-backend-item__stats">
                <div>
                  <span class="diagnostic-backend-item__label">平均</span>
                  <strong class="diagnostic-backend-item__value">{{ backend.summary?.avg_latency_ms ?? 0 }} ms</strong>
                </div>
                <div>
                  <span class="diagnostic-backend-item__label">成功</span>
                  <strong class="diagnostic-backend-item__value">{{ backend.summary?.succeeded ?? 0 }} / {{ backend.summary?.sent ?? 0 }}</strong>
                </div>
              </div>

              <div class="diagnostic-backend-item__range">
                <span>最小 {{ backend.summary?.min_latency_ms ?? 0 }} ms</span>
                <span>最大 {{ backend.summary?.max_latency_ms ?? 0 }} ms</span>
              </div>

              <div class="diagnostic-backend-item__adaptive-summary">
                <span class="diagnostic-badge">状态 {{ adaptiveStateLabel(backend.adaptive?.state) }}</span>
                <span class="diagnostic-badge">24h稳定性 {{ formatPercent(backend.adaptive?.stability) }}</span>
                <span class="diagnostic-badge">置信度 {{ formatPercent(backend.adaptive?.sample_confidence) }}</span>
                <span class="diagnostic-badge">慢启动 {{ slowStartLabel(backend.adaptive?.slow_start_active) }}</span>
              </div>

              <button type="button" class="diagnostic-backend-item__toggle" @click="toggleAdaptive(backend.backend)">
                <span>{{ isAdaptiveExpanded(backend.backend) ? '收起' : '展开更多' }}</span>
                <span class="diagnostic-backend-item__toggle-icon" :class="{ 'diagnostic-backend-item__toggle-icon--open': isAdaptiveExpanded(backend.backend) }">▸</span>
              </button>

              <div v-show="isAdaptiveExpanded(backend.backend)" class="diagnostic-backend-item__details">
                <div class="diagnostic-backend-item__details-grid">
                  <div class="diagnostic-factor">
                    <span class="diagnostic-factor__label">延迟</span>
                    <strong class="diagnostic-factor__value">{{ backend.adaptive?.latency_ms ?? 0 }} ms</strong>
                  </div>
                  <div class="diagnostic-factor">
                    <span class="diagnostic-factor__label">评估带宽</span>
                    <strong class="diagnostic-factor__value">{{ formatBandwidth(backend.adaptive?.estimated_bandwidth_bps) }}</strong>
                  </div>
                  <div class="diagnostic-factor">
                    <span class="diagnostic-factor__label">综合性能</span>
                    <strong class="diagnostic-factor__value">{{ formatScore(backend.adaptive?.performance_score) }}</strong>
                  </div>
                  <div class="diagnostic-factor">
                    <span class="diagnostic-factor__label">异常检测</span>
                    <strong class="diagnostic-factor__value">{{ outlierLabel(backend.adaptive?.outlier) }}</strong>
                  </div>
                  <div class="diagnostic-factor">
                    <span class="diagnostic-factor__label">流量阶段</span>
                    <strong class="diagnostic-factor__value">{{ trafficShareLabel(backend.adaptive?.traffic_share_hint) }}</strong>
                  </div>
                </div>
                <div v-if="backend.adaptive?.reason" class="diagnostic-backend-item__reason">
                  原因: {{ reasonLabel(backend.adaptive?.reason) }}
                </div>
              </div>

              <div v-if="backend.children?.length" class="diagnostic-backend-item__children">
                <div class="diagnostic-backend-item__child-title">已解析候选</div>
                <div class="diagnostic-child-list">
                  <div v-for="child in backend.children" :key="child.backend" class="diagnostic-child-row">
                    <code class="diagnostic-child-row__name">{{ child.backend }}</code>
                    <span v-if="child.adaptive?.preferred" class="diagnostic-backend-item__preferred">当前优选</span>
                    <span class="diagnostic-badge diagnostic-badge--subtle">状态 {{ adaptiveStateLabel(child.adaptive?.state) }}</span>
                    <span class="diagnostic-badge diagnostic-badge--subtle">延迟 {{ child.adaptive?.latency_ms ?? 0 }} ms</span>
                    <span class="diagnostic-badge diagnostic-badge--subtle">置信度 {{ formatPercent(child.adaptive?.sample_confidence) }}</span>
                  </div>
                </div>
              </div>
            </article>
          </div>
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
                <code class="diagnostic-sample__backend">{{ sample.backend || '-' }}</code>
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
  endpointLabel: { type: String, default: '' }
})

defineEmits(['update:modelValue'])

const state = computed(() => props.task?.state || 'pending')
const busy = computed(() => !['completed', 'failed'].includes(state.value))
const summary = computed(() => props.task?.result?.summary || null)
const backendSummaries = computed(() => props.task?.result?.backends || [])
const samples = computed(() => props.task?.result?.samples || [])
const title = computed(() => props.kind === 'l4_tcp' ? 'L4 规则诊断' : 'HTTP 规则诊断')
const kindLabel = computed(() => props.kind === 'l4_tcp' ? 'TCP PATH DIAGNOSIS' : 'HTTP PATH DIAGNOSIS')
const stateLabel = computed(() => diagnosticStateLabel(state.value))
const tone = computed(() => diagnosticStateTone(state.value))
const agentLabel = computed(() => props.task?.agent_id || '')
const isHTTP = computed(() => props.kind === 'http')
const showSamples = ref(false)
const expandedAdaptive = ref(new Set())

function toggleAdaptive(backendName) {
  const s = new Set(expandedAdaptive.value)
  if (s.has(backendName)) s.delete(backendName)
  else s.add(backendName)
  expandedAdaptive.value = s
}

function isAdaptiveExpanded(backendName) {
  return expandedAdaptive.value.has(backendName)
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

const latencyBarPct = computed(() => {
  if (!summary.value) return 0
  const min = summary.value.min_latency_ms ?? 0
  const max = summary.value.max_latency_ms ?? 0
  const avg = summary.value.avg_latency_ms ?? 0
  if (max <= min) return 50
  const pct = ((avg - min) / (max - min)) * 100
  return Math.max(8, Math.min(92, pct))
})

function formatPercent(value) {
  if (value == null) return '-'
  return `${Math.round(Number(value) * 100)}%`
}

function formatBandwidth(value) {
  const num = Number(value || 0)
  if (!num) return '-'
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
.diagnostic-modal { display: flex; flex-direction: column; gap: 1.1rem; }
.diagnostic-modal__hero {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 1.1rem 1.25rem;
  border-radius: 16px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  box-shadow: inset 0 1px 0 rgba(255,255,255,0.04);
  position: relative;
  overflow: hidden;
}
.diagnostic-modal__hero::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 4px;
  background: var(--color-primary);
  opacity: 0.6;
}
.diagnostic-modal__hero-text { min-width: 0; }
.diagnostic-modal__eyebrow { font-size: 0.7rem; letter-spacing: 0.08em; color: var(--color-text-tertiary); font-weight: 600; }
.diagnostic-modal__headline { margin: 0.25rem 0 0.3rem; font-size: 1.05rem; font-weight: 700; color: var(--color-text-primary); line-height: 1.35; }
.diagnostic-modal__subtitle { margin: 0; font-family: var(--font-mono); font-size: 0.8rem; color: var(--color-text-secondary); word-break: break-all; line-height: 1.4; }
.diagnostic-modal__meta { margin: 0.4rem 0 0; font-size: 0.75rem; color: var(--color-text-tertiary); }
.diagnostic-modal__state { align-self: flex-start; padding: 0.3rem 0.75rem; border-radius: 999px; font-size: 0.78rem; font-weight: 700; border: 1px solid transparent; }
.diagnostic-modal__state--success { background: rgba(16, 185, 129, 0.12); color: var(--color-success); border-color: rgba(16, 185, 129, 0.25); }
.diagnostic-modal__state--danger { background: rgba(239, 68, 68, 0.12); color: var(--color-danger); border-color: rgba(239, 68, 68, 0.25); }
.diagnostic-modal__state--info { background: rgba(56, 189, 248, 0.12); color: var(--color-primary); border-color: rgba(56, 189, 248, 0.25); }
.diagnostic-modal__state--muted { background: var(--color-bg-hover); color: var(--color-text-muted); border-color: var(--color-border-subtle); }
.diagnostic-modal__loading, .diagnostic-modal__error {
  display: flex;
  align-items: center;
  gap: 0.875rem;
  padding: 1rem 1.125rem;
  border-radius: 14px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
}
.diagnostic-modal__error { color: var(--color-danger); border-color: rgba(239, 68, 68, 0.25); }
.diagnostic-modal__pulse {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--color-primary);
  box-shadow: 0 0 0 rgba(13, 148, 136, 0.35);
  animation: diag-pulse 1.5s infinite;
}
.diagnostic-modal__loading-title { font-weight: 700; color: var(--color-text-primary); }
.diagnostic-modal__loading-text { font-size: 0.88rem; color: var(--color-text-secondary); }
.diagnostic-modal__stats {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.7rem;
}
@media (min-width: 520px) {
  .diagnostic-modal__stats {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}
.diagnostic-stat {
  padding: 0.75rem 0.9rem;
  border-radius: 12px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}
.diagnostic-stat__label { font-size: 0.72rem; color: var(--color-text-tertiary); font-weight: 500; }
.diagnostic-stat__value { font-size: 1rem; color: var(--color-text-primary); font-weight: 700; }
.diagnostic-stat__value--caps { text-transform: uppercase; letter-spacing: 0.06em; font-size: 0.92rem; }
.diagnostic-stat__value--success { color: var(--color-success); }
.diagnostic-stat__value--info { color: var(--color-primary); }
.diagnostic-stat__value--warning { color: var(--color-warning); }
.diagnostic-stat__value--danger { color: var(--color-danger); }
.diagnostic-stat__value--muted { color: var(--color-text-muted); }
.diagnostic-modal__range {
  padding: 0 0.1rem;
}
.latency-bar { display: flex; flex-direction: column; gap: 0.35rem; }
.latency-bar__track {
  height: 6px;
  border-radius: 999px;
  background: var(--color-bg-hover);
  overflow: hidden;
}
.latency-bar__fill {
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(90deg, var(--color-success), var(--color-primary));
  transition: width 0.4s ease;
}
.latency-bar__labels {
  display: flex;
  justify-content: space-between;
  font-size: 0.78rem;
  color: var(--color-text-tertiary);
}
.diagnostic-modal__backends {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.diagnostic-backend-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 0.75rem;
}
.diagnostic-backend-card {
  padding: 0.85rem 0.95rem;
  border-radius: 12px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
}
.diagnostic-backend-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.75rem;
}
.diagnostic-backend-card__badges {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.35rem;
}
.diagnostic-backend-card__name {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--color-text-primary);
  word-break: break-all;
}
.diagnostic-backend-card__preferred {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 0.68rem;
  font-weight: 700;
  background: rgba(16, 185, 129, 0.12);
  color: var(--color-success);
  border: 1px solid rgba(16, 185, 129, 0.2);
}
.diagnostic-backend-card__quality {
  flex-shrink: 0;
  font-size: 0.68rem;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--color-bg-hover);
  color: var(--color-text-muted);
}
.diagnostic-backend-card__quality--success { background: var(--color-success-50); color: var(--color-success); }
.diagnostic-backend-card__quality--info { background: rgba(56, 189, 248, 0.12); color: var(--color-primary); }
.diagnostic-backend-card__quality--warning { background: var(--color-warning-50); color: var(--color-warning); }
.diagnostic-backend-card__quality--danger { background: var(--color-danger-50); color: var(--color-danger); }
.diagnostic-backend-card__stats {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.6rem;
}
.diagnostic-backend-card__label {
  display: block;
  font-size: 0.72rem;
  color: var(--color-text-tertiary);
  margin-bottom: 0.15rem;
}
.diagnostic-backend-card__value {
  font-size: 0.95rem;
  color: var(--color-text-primary);
}
.diagnostic-backend-card__range {
  display: flex;
  justify-content: space-between;
  gap: 0.75rem;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
.diagnostic-backend-card__adaptive {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.45rem;
}
.diagnostic-backend-card__adaptive--child {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.diagnostic-factor {
  padding: 0.45rem 0.55rem;
  border-radius: 10px;
  background: var(--color-bg-hover);
  border: 1px solid var(--color-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}
.diagnostic-factor__label {
  font-size: 0.68rem;
  color: var(--color-text-tertiary);
}
.diagnostic-factor__value {
  font-size: 0.86rem;
  color: var(--color-text-primary);
}
.diagnostic-backend-card__reason {
  font-size: 0.74rem;
  color: var(--color-text-secondary);
}
.diagnostic-backend-card__children {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding-top: 0.2rem;
  border-top: 1px solid var(--color-border-subtle);
}
.diagnostic-backend-card__child-title {
  font-size: 0.72rem;
  font-weight: 700;
  color: var(--color-text-tertiary);
}
.diagnostic-backend-child {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
  padding: 0.6rem;
  border-radius: 10px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
}
.diagnostic-backend-child__header {
  display: flex;
  justify-content: space-between;
  gap: 0.5rem;
  align-items: flex-start;
}
.diagnostic-backend-child__name {
  font-family: var(--font-mono);
  font-size: 0.74rem;
  color: var(--color-text-secondary);
  word-break: break-all;
}
.diagnostic-modal__section-title { font-weight: 700; color: var(--color-text-primary); margin-bottom: 0.5rem; display: flex; align-items: center; gap: 0.5rem; }
.diagnostic-modal__section-title--toggle {
  width: 100%;
  background: transparent;
  border: none;
  padding: 0.35rem 0;
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
.diagnostic-modal__sample-count { font-size: 0.72rem; font-weight: 600; color: var(--color-text-tertiary); background: var(--color-bg-hover); padding: 2px 8px; border-radius: 999px; }
.diagnostic-modal__samples { display: flex; flex-direction: column; }
.diagnostic-sample-list {
  max-height: 220px;
  overflow-y: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: 12px;
  padding: 0.3rem 0.4rem;
  background: var(--color-bg-surface);
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
  align-items: center;
}
.diagnostic-sample__attempt { font-size: 0.72rem; color: var(--color-text-tertiary); font-family: var(--font-mono); min-width: 2.2ch; text-align: right; }
.diagnostic-sample__status { font-size: 0.65rem; font-weight: 700; padding: 1px 4px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.diagnostic-sample__status--success { background: var(--color-success-50); color: var(--color-success); }
.diagnostic-sample__status--info { background: rgba(56, 189, 248, 0.12); color: #0ea5e9; }
.diagnostic-sample__status--warning { background: var(--color-warning-50); color: var(--color-warning); }
.diagnostic-sample__status--danger { background: var(--color-danger-50); color: var(--color-danger); }
.diagnostic-sample__status--muted { background: var(--color-bg-hover); color: var(--color-text-muted); }
.diagnostic-sample__backend {
  font-family: var(--font-mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 0.78rem;
  color: var(--color-text-secondary);
}
.diagnostic-sample__right {
  font-size: 0.78rem;
  color: var(--color-text-secondary);
  white-space: nowrap;
  font-family: var(--font-mono);
}
.diagnostic-sample--failed .diagnostic-sample__right { font-weight: 500; }
@keyframes diag-pulse {
  0% { box-shadow: 0 0 0 0 rgba(13, 148, 136, 0.35); }
  70% { box-shadow: 0 0 0 14px rgba(13, 148, 136, 0); }
  100% { box-shadow: 0 0 0 0 rgba(13, 148, 256, 0); }
}
</style>
