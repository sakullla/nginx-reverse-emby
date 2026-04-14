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
        <div>
          <div class="diagnostic-modal__eyebrow">{{ kindLabel }}</div>
          <h3 class="diagnostic-modal__headline">{{ ruleLabel }}</h3>
          <p class="diagnostic-modal__subtitle">{{ endpointLabel }}</p>
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
            <strong class="diagnostic-stat__value diagnostic-stat__value--caps">{{ summary.quality || '-' }}</strong>
          </div>
        </div>

        <div class="diagnostic-modal__range">
          <span>最小 {{ summary.min_latency_ms ?? 0 }} ms</span>
          <span>最大 {{ summary.max_latency_ms ?? 0 }} ms</span>
        </div>

        <div class="diagnostic-modal__samples">
          <div class="diagnostic-modal__section-title">探测样本</div>
          <div class="diagnostic-sample" v-for="sample in samples" :key="`${sample.attempt}-${sample.backend}`" :class="{ 'diagnostic-sample--failed': !sample.success }">
            <div class="diagnostic-sample__left">
              <span class="diagnostic-sample__attempt">#{{ sample.attempt }}</span>
              <code class="diagnostic-sample__backend">{{ sample.backend || '-' }}</code>
            </div>
            <div class="diagnostic-sample__right">
              <span v-if="sample.success">{{ sample.latency_ms }} ms</span>
              <span v-else>{{ sample.error || 'failed' }}</span>
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
const samples = computed(() => props.task?.result?.samples || [])
const title = computed(() => props.kind === 'l4_tcp' ? 'L4 规则诊断' : 'HTTP 规则诊断')
const kindLabel = computed(() => props.kind === 'l4_tcp' ? 'TCP PATH DIAGNOSIS' : 'HTTP PATH DIAGNOSIS')
const stateLabel = computed(() => diagnosticStateLabel(state.value))
const tone = computed(() => diagnosticStateTone(state.value))

function formatPercent(value) {
  if (value == null) return '-'
  return `${Math.round(Number(value) * 100)}%`
}
</script>

<style scoped>
.diagnostic-modal { display: flex; flex-direction: column; gap: 1rem; }
.diagnostic-modal__hero {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.125rem;
  border-radius: 20px;
  background:
    radial-gradient(circle at top left, rgba(13, 148, 136, 0.12), transparent 55%),
    linear-gradient(135deg, rgba(15, 23, 42, 0.98), rgba(30, 41, 59, 0.92));
  color: #f8fafc;
}
.diagnostic-modal__eyebrow { font-size: 0.72rem; letter-spacing: 0.12em; color: rgba(226, 232, 240, 0.68); }
.diagnostic-modal__headline { margin: 0.2rem 0 0.35rem; font-size: 1.15rem; }
.diagnostic-modal__subtitle { margin: 0; font-family: var(--font-mono); font-size: 0.82rem; color: rgba(226, 232, 240, 0.8); word-break: break-all; }
.diagnostic-modal__state { align-self: flex-start; padding: 0.35rem 0.8rem; border-radius: 999px; font-size: 0.82rem; font-weight: 700; }
.diagnostic-modal__state--success { background: rgba(16, 185, 129, 0.2); color: #6ee7b7; }
.diagnostic-modal__state--danger { background: rgba(239, 68, 68, 0.18); color: #fca5a5; }
.diagnostic-modal__state--info { background: rgba(56, 189, 248, 0.16); color: #7dd3fc; }
.diagnostic-modal__state--muted { background: rgba(148, 163, 184, 0.16); color: #cbd5e1; }
.diagnostic-modal__loading, .diagnostic-modal__error {
  display: flex;
  align-items: center;
  gap: 0.875rem;
  padding: 1rem 1.125rem;
  border-radius: 18px;
  background: var(--color-bg-subtle);
}
.diagnostic-modal__error { color: var(--color-danger); border: 1px solid rgba(239, 68, 68, 0.2); }
.diagnostic-modal__pulse {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #0f766e;
  box-shadow: 0 0 0 rgba(15, 118, 110, 0.4);
  animation: diag-pulse 1.5s infinite;
}
.diagnostic-modal__loading-title { font-weight: 700; color: var(--color-text-primary); }
.diagnostic-modal__loading-text { font-size: 0.88rem; color: var(--color-text-secondary); }
.diagnostic-modal__stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 0.75rem;
}
.diagnostic-stat {
  padding: 0.9rem 1rem;
  border-radius: 18px;
  background: linear-gradient(180deg, rgba(248, 250, 252, 0.94), rgba(241, 245, 249, 0.9));
  border: 1px solid var(--color-border-default);
}
.diagnostic-stat__label { display: block; font-size: 0.76rem; color: var(--color-text-tertiary); margin-bottom: 0.35rem; }
.diagnostic-stat__value { font-size: 1.05rem; color: var(--color-text-primary); }
.diagnostic-stat__value--caps { text-transform: uppercase; letter-spacing: 0.08em; }
.diagnostic-modal__range {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  font-size: 0.82rem;
  color: var(--color-text-secondary);
}
.diagnostic-modal__section-title { font-weight: 700; color: var(--color-text-primary); margin-bottom: 0.5rem; }
.diagnostic-modal__samples { display: flex; flex-direction: column; }
.diagnostic-sample {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.7rem 0.85rem;
  border-top: 1px solid var(--color-border-subtle);
}
.diagnostic-sample:first-of-type { border-top: none; }
.diagnostic-sample--failed { color: var(--color-danger); }
.diagnostic-sample__left {
  display: flex;
  gap: 0.75rem;
  min-width: 0;
  align-items: center;
}
.diagnostic-sample__attempt { font-size: 0.78rem; color: var(--color-text-tertiary); }
.diagnostic-sample__backend {
  font-family: var(--font-mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
@keyframes diag-pulse {
  0% { box-shadow: 0 0 0 0 rgba(15, 118, 110, 0.45); }
  70% { box-shadow: 0 0 0 14px rgba(15, 118, 110, 0); }
  100% { box-shadow: 0 0 0 0 rgba(15, 118, 110, 0); }
}
</style>
