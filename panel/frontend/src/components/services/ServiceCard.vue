<template>
  <BaseListCard
    :status="cardStatus"
    clickable
    class="service-card"
    data-testid="service-card"
    @click="$emit('detail', service)"
  >
    <template #header-left>
      <h2 class="service-card__title" :title="serviceTitle">{{ serviceTitle }}</h2>
      <BaseBadge :tone="summaryTone" dot data-testid="service-summary">{{ summaryLabel }}</BaseBadge>
      <BaseBadge
        v-if="service.switch_phase"
        tone="warning"
        size="sm"
        data-testid="service-switch-phase"
      >
        {{ service.switch_phase }}
      </BaseBadge>
    </template>

    <template #header-right>
      <a
        v-if="openHref"
        class="service-card__icon-link"
        :href="openHref"
        target="_blank"
        rel="noopener"
        title="打开访问地址"
        aria-label="打开访问地址"
        data-action="open"
        @click.stop
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
          <polyline points="15 3 21 3 21 9" />
          <line x1="10" y1="14" x2="21" y2="3" />
        </svg>
      </a>
      <BaseIconButton
        title="修改"
        data-action="edit"
        @click="$emit('edit', service)"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
        </svg>
      </BaseIconButton>
      <BaseActionMenu :items="menuItems" @select="onMenuSelect" />
    </template>

    <div class="service-card__endpoint">
      <div class="service-card__endpoint-main">
        <span class="service-card__endpoint-label">访问地址</span>
        <code class="service-card__url">{{ service.entry_url || '—' }}</code>
      </div>
      <CopyButton
        :value="service.entry_url"
        label="复制访问地址"
        success-message="已复制访问地址"
        size="xs"
        data-testid="service-card-copy"
      />
    </div>

    <dl class="service-card__meta">
      <div class="service-card__row">
        <dt>节点</dt>
        <dd class="service-card__nodes" data-testid="service-nodes">{{ nodesSummary }}</dd>
      </div>
      <div class="service-card__row">
        <dt>路径</dt>
        <dd data-testid="service-path" class="service-card__path">{{ pathLabel }}</dd>
      </div>
      <div class="service-card__row">
        <dt>证书</dt>
        <dd data-testid="service-certificate" class="service-card__cert">
          <BaseBadge :tone="certTone" size="sm">{{ certificate.label }}</BaseBadge>
          <span v-if="certificate.detail" class="service-card__cert-detail">：{{ certificate.detail }}</span>
        </dd>
      </div>
      <div class="service-card__row">
        <dt>流量</dt>
        <dd data-testid="service-traffic" class="service-card__traffic">
          <span class="service-card__mono">{{ traffic.text }}</span>
          <span v-if="traffic.detail" class="service-card__traffic-detail"> · {{ traffic.detail }}</span>
        </dd>
      </div>
    </dl>

    <div
      v-if="steps.length"
      class="service-card__steps"
      :class="{ 'service-card__steps--danger': summaryTone === 'danger' }"
      data-testid="service-steps"
    >
      <svg class="service-card__steps-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <line x1="12" y1="8" x2="12" y2="12" />
        <line x1="12" y1="16" x2="12.01" y2="16" />
      </svg>
      <div class="service-card__steps-body">
        <span class="service-card__steps-label">未完成环节</span>
        <span class="service-card__steps-text">{{ steps.join('；') }}</span>
      </div>
    </div>

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
          <BaseBadge
            v-if="probe.verification"
            :tone="probe.verification === 'expired' ? 'warning' : 'neutral'"
            size="sm"
            mono
          >
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

    <template v-if="hasFooter" #footer>
      <BaseBadge v-if="service.client_confirmation?.confirmed" tone="success" size="sm" dot>
        用户确认页面可打开
      </BaseBadge>
      <BaseBadge v-if="service.advanced" tone="warning" size="sm">
        高级规则
      </BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import BaseActionMenu from '../base/BaseActionMenu.vue'
import CopyButton from '../common/CopyButton.vue'
import { isCopyableText } from '../../utils/clipboard.js'
import {
  certificateText,
  certificateTone,
  incompleteSteps,
  pathText,
  probeText,
  probeTone,
  safeHttpHref,
  serviceSummaryTone,
  serviceTrafficText,
} from '../../utils/accessService.js'

const props = defineProps({
  service: { type: Object, required: true },
  entryAgentLabel: { type: String, default: '' },
  serviceAgentLabel: { type: String, default: '' },
})

const emit = defineEmits(['copy', 'detail', 'edit', 'delete'])

const serviceTitle = computed(() => props.service?.name || '未命名服务')
const summaryLabel = computed(() => props.service?.summary || '控制面没有返回摘要')
const summaryTone = computed(() => serviceSummaryTone(props.service))
const cardStatus = computed(() => (summaryTone.value === 'primary' ? 'neutral' : summaryTone.value))
const pathLabel = computed(() => pathText(props.service?.path))
const steps = computed(() => incompleteSteps(props.service))
const certificate = computed(() => certificateText(props.service?.certificate))
const certTone = computed(() => certificateTone(props.service?.certificate))
const traffic = computed(() => serviceTrafficText(props.service?.traffic))
const probes = computed(() => (
  Array.isArray(props.service?.probes) ? props.service.probes : []
).map((probe) => probeText(probe)))
const openHref = computed(() => safeHttpHref(props.service?.entry_url))
const hasCopyableUrl = computed(() => isCopyableText(String(props.service?.entry_url || '').trim()))
const hasFooter = computed(() => Boolean(props.service?.client_confirmation?.confirmed || props.service?.advanced))

const nodesSummary = computed(() => {
  const entry = props.entryAgentLabel || props.service?.entry_agent_id || '—'
  const serviceNode = props.serviceAgentLabel || props.service?.service_agent_id || ''
  if (!serviceNode || String(props.service?.entry_agent_id || '') === String(props.service?.service_agent_id || '')) {
    return entry
  }
  return `${entry} → ${serviceNode}`
})

const menuItems = computed(() => [
  { id: 'detail', label: '查看详情' },
  { id: 'copy', label: '复制地址', disabled: !hasCopyableUrl.value },
  { id: 'delete', label: '删除', tone: 'danger' },
])

function onMenuSelect(item) {
  if (item?.id === 'detail') emit('detail', props.service)
  else if (item?.id === 'copy') emit('copy', props.service)
  else if (item?.id === 'delete') emit('delete', props.service)
}
</script>

<style scoped>
.service-card__title {
  margin: 0;
  min-width: 0;
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.35;
  overflow-wrap: anywhere;
}

.service-card__icon-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
  text-decoration: none;
  flex-shrink: 0;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default),
              transform var(--duration-fast) var(--ease-default);
}

.service-card__icon-link:hover {
  background: var(--color-bg-hover);
  color: var(--color-primary);
}

.service-card__icon-link:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 2px var(--color-primary);
}

.service-card__icon-link:active {
  transform: scale(0.95);
}

.service-card__endpoint {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-2) var(--space-2-5);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}

.service-card__endpoint-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1 1 auto;
  min-width: 0;
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
  gap: var(--space-1-5);
  margin: 0;
}

.service-card__row {
  display: grid;
  grid-template-columns: 3.75rem minmax(0, 1fr);
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

.service-card__steps {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-2-5);
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-warning) 28%, transparent);
  background: var(--color-warning-50);
  color: var(--color-warning);
  font-size: var(--text-xs);
  line-height: 1.45;
  min-width: 0;
}

.service-card__steps--danger {
  border-color: color-mix(in srgb, var(--color-danger) 28%, transparent);
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.service-card__steps-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.service-card__steps-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.service-card__steps-label {
  font-size: var(--text-2xs);
  font-weight: var(--font-semibold);
}

.service-card__steps-text {
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
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

@media (pointer: coarse) {
  .service-card__icon-link {
    width: 44px;
    height: 44px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .service-card__icon-link {
    transition: none;
  }
}
</style>
