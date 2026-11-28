<template>
  <article class="service-card" :data-status="cardStatus" data-testid="service-card">
    <header class="service-card__header">
      <h2 class="service-card__title">{{ service.name || '未命名服务' }}</h2>
      <BaseBadge :tone="summaryTone" dot data-testid="service-summary">{{ summaryLabel }}</BaseBadge>
    </header>

    <div class="service-card__endpoint">
      <span class="service-card__endpoint-label">访问地址</span>
      <code class="service-card__url">{{ service.entry_url || '—' }}</code>
    </div>

    <dl class="service-card__meta">
      <div class="service-card__row">
        <dt>入口节点</dt>
        <dd>{{ entryAgentLabel || service.entry_agent_id || '—' }}</dd>
      </div>
      <div class="service-card__row">
        <dt>路径</dt>
        <dd data-testid="service-path" class="service-card__path">{{ pathLabel }}</dd>
      </div>
      <div class="service-card__row">
        <dt>未完成环节</dt>
        <dd data-testid="service-steps" :class="{ 'service-card__steps--pending': steps.length }">
          {{ steps.length ? steps.join('；') : '无' }}
        </dd>
      </div>
      <div v-if="service.switch_phase" class="service-card__row">
        <dt>切换阶段</dt>
        <dd>
          <BaseBadge tone="warning" size="sm">{{ service.switch_phase }}</BaseBadge>
        </dd>
      </div>
      <div class="service-card__row">
        <dt>证书</dt>
        <dd data-testid="service-certificate" class="service-card__cert">
          <BaseBadge :tone="certificateTone" size="sm">{{ certificate.label }}</BaseBadge><span v-if="certificate.detail" class="service-card__cert-detail">：{{ certificate.detail }}</span>
        </dd>
      </div>
      <div class="service-card__row">
        <dt>流量</dt>
        <dd data-testid="service-traffic" class="service-card__traffic">
          <span class="service-card__mono">{{ traffic.text }}</span><span v-if="traffic.detail" class="service-card__traffic-detail"> · {{ traffic.detail }}</span>
        </dd>
      </div>
    </dl>

    <ul v-if="probes.length" class="service-card__probes">
      <li
        v-for="(probe, index) in probes"
        :key="`${probe.agentId}-${probe.purpose}-${index}`"
        class="service-card__probe"
        data-testid="service-probe"
      >
        <span class="service-card__probe-head">
          <span class="service-card__probe-node">节点探测 {{ probe.agentId || '未知节点' }}</span>
          <BaseBadge :tone="probeTone(probe)" size="sm" mono>{{ probe.text }}</BaseBadge>
          <BaseBadge v-if="probe.verification" :tone="probe.verification === 'expired' ? 'warning' : 'neutral'" size="sm" mono>
            {{ probe.verification }}
          </BaseBadge>
        </span>
        <span v-if="probe.target || probe.checkedAt" class="service-card__probe-meta">
          <code v-if="probe.target" class="service-card__probe-target">{{ probe.target }}</code>
          <span v-if="probe.checkedAt" class="service-card__probe-time">
            <template v-if="probe.target"> · </template>{{ probe.checkedAt }}
          </span>
        </span>
      </li>
    </ul>

    <div v-if="service.client_confirmation?.confirmed" class="service-card__confirm">
      <BaseBadge tone="success" size="sm" dot>用户确认页面可打开</BaseBadge>
    </div>

    <footer class="service-card__actions">
      <div class="service-card__actions-primary">
        <button type="button" class="btn btn-primary btn-sm" @click="$emit('detail', service)">详情</button>
        <a v-if="openHref" class="btn btn-secondary btn-sm" :href="openHref" target="_blank" rel="noopener">打开</a>
        <button type="button" class="btn btn-secondary btn-sm" @click="$emit('copy', service)">复制</button>
        <button type="button" class="btn btn-secondary btn-sm" @click="$emit('edit', service)">修改</button>
      </div>
      <button type="button" class="btn btn--danger-soft btn-sm service-card__delete" @click="$emit('delete', service)">删除</button>
    </footer>
  </article>
</template>

<script setup>
import { computed } from 'vue'
import BaseBadge from '../base/BaseBadge.vue'
import {
  certificateText,
  incompleteSteps,
  pathText,
  probeText,
  safeHttpHref,
  serviceTrafficText,
} from '../../utils/accessService.js'

const props = defineProps({
  service: { type: Object, required: true },
  entryAgentLabel: { type: String, default: '' },
})

defineEmits(['copy', 'detail', 'edit', 'delete'])

const summaryLabel = computed(() => props.service?.summary || '控制面没有返回摘要')
const summaryTone = computed(() => {
  const summary = String(props.service?.summary || '').trim()
  if (summary === '已就绪') return 'success'
  if (summary === '失败') return 'danger'
  if (summary && (summary.includes('待') || summary.includes('中'))) return 'warning'
  return 'neutral'
})
const cardStatus = computed(() => (summaryTone.value === 'primary' ? 'neutral' : summaryTone.value))
const pathLabel = computed(() => pathText(props.service?.path))
const steps = computed(() => incompleteSteps(props.service))
const certificate = computed(() => certificateText(props.service?.certificate))
const certificateTone = computed(() => {
  const state = String(props.service?.certificate?.state || 'none')
  if (state === 'ready') return 'success'
  if (state === 'error') return 'danger'
  if (state === 'issuing') return 'primary'
  if (state === 'none') return 'neutral'
  return 'warning'
})
const traffic = computed(() => serviceTrafficText(props.service?.traffic))
const probes = computed(() => (Array.isArray(props.service?.probes) ? props.service.probes : []).map((probe) => probeText(probe)))
const openHref = computed(() => safeHttpHref(props.service?.entry_url))

function probeTone(probe) {
  if (probe.verification === 'expired') return 'warning'
  if (probe.error) return 'danger'
  if (probe.status === 'connected') return 'success'
  if (probe.status === 'http_status') return 'primary'
  if (probe.status === 'untested') return 'neutral'
  return 'warning'
}
</script>

<style scoped>
.service-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
  padding: var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-normal) var(--ease-default);
}

.service-card:hover {
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-md);
}

.service-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
  min-width: 0;
}

.service-card__title {
  margin: 0;
  min-width: 0;
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.35;
  overflow-wrap: anywhere;
}

.service-card__endpoint {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}

.service-card__endpoint-label {
  font-size: var(--text-2xs);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
  line-height: 1.2;
}

.service-card__url {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  line-height: 1.45;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-card__meta {
  display: grid;
  gap: var(--space-2);
  margin: 0;
}

.service-card__row {
  display: grid;
  grid-template-columns: 5.5rem minmax(0, 1fr);
  align-items: baseline;
  gap: var(--space-2);
  font-size: var(--text-xs);
  line-height: 1.45;
}

.service-card__row dt {
  color: var(--color-text-muted);
  font-weight: var(--font-medium);
}

.service-card__row dd {
  margin: 0;
  min-width: 0;
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-card__steps--pending {
  color: var(--color-warning);
}

.service-card__cert {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
}

.service-card__cert-detail {
  color: var(--color-text-secondary);
}

.service-card__mono {
  font-family: var(--font-mono);
}

.service-card__traffic-detail {
  color: var(--color-text-tertiary);
}

.service-card__probes {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
}

.service-card__probe {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  padding: var(--space-2) var(--space-2-5);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

.service-card__probe-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-card__probe-node {
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.service-card__probe-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1);
  color: var(--color-text-tertiary);
  overflow-wrap: anywhere;
}

.service-card__probe-target {
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--color-text-secondary);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-card__probe-time {
  font-size: var(--text-2xs);
  color: var(--color-text-muted);
}

.service-card__confirm {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
}

.service-card__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-top: auto;
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
}

.service-card__actions-primary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

@media (max-width: 640px) {
  .service-card {
    padding: var(--space-3-5, 0.875rem);
  }

  .service-card__row {
    grid-template-columns: 4.75rem minmax(0, 1fr);
  }

  .service-card__actions {
    gap: var(--space-1-5);
  }
}

@media (prefers-reduced-motion: reduce) {
  .service-card {
    transition: none;
  }
}
</style>
