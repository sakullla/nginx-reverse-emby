<template>
  <section class="service-detail" data-testid="service-detail">
    <div class="service-detail__topbar">
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        data-testid="detail-back"
        @click="$emit('back')"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="m15 18-6-6 6-6" />
        </svg>
        <span>返回列表</span>
      </button>

      <div class="service-detail__top-actions">
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="refreshing || submitting"
          data-testid="detail-refresh"
          @click="$emit('refresh')"
        >
          <svg
            :class="{ 'service-detail__spin': refreshing }"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M21 12a9 9 0 1 1-2.64-6.36" />
            <polyline points="21 3 21 9 15 9" />
          </svg>
          <span>{{ refreshing ? '刷新中...' : '刷新' }}</span>
        </button>

        <button
          v-if="showRetry"
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="submitting"
          data-testid="detail-retry"
          @click="$emit('retry')"
        >
          重试
        </button>
        <button
          v-if="service.switch_phase === '退役旧入口'"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="submitting"
          @click="$emit('retire')"
        >
          退役旧入口
        </button>
        <button
          v-if="service.switch_phase === '准备新入口' || service.switch_phase === '等待新入口生效'"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="submitting"
          @click="$emit('abandon')"
        >
          放弃这次切换
        </button>
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          @click="$emit('edit', service)"
        >
          修改
        </button>
        <button
          type="button"
          class="btn btn--danger-soft btn-sm"
          @click="$emit('delete', service)"
        >
          删除
        </button>
      </div>
    </div>

    <header class="service-detail__hero">
      <div class="service-detail__hero-head">
        <h2 class="service-detail__title">{{ service.name || '未命名服务' }}</h2>
        <div class="service-detail__badges">
          <BaseBadge :tone="summaryTone" size="md" dot data-testid="detail-summary">
            {{ service.summary || '控制面没有返回摘要' }}
          </BaseBadge>
          <BaseBadge v-if="service.switch_phase" tone="warning" size="md">
            {{ service.switch_phase }}
          </BaseBadge>
          <BaseBadge v-if="service.advanced" tone="warning" size="md">
            高级规则
          </BaseBadge>
        </div>
      </div>

      <div class="service-detail__endpoint">
        <div class="service-detail__endpoint-main">
          <span class="service-detail__endpoint-label">访问地址</span>
          <code class="service-detail__endpoint-url">{{ service.entry_url || '—' }}</code>
        </div>
        <div v-if="service.entry_url || openHref" class="service-detail__endpoint-actions">
          <CopyButton
            :value="service.entry_url"
            label="复制访问地址"
            success-message="已复制访问地址"
            size="sm"
            data-testid="detail-hero-copy"
          />
          <a
            v-if="openHref"
            class="btn btn-secondary btn-sm"
            :href="openHref"
            target="_blank"
            rel="noopener"
            data-testid="detail-hero-open"
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
              <polyline points="15 3 21 3 21 9" />
              <line x1="10" y1="14" x2="21" y2="3" />
            </svg>
            <span>打开</span>
          </a>
        </div>
      </div>
    </header>

    <section class="service-detail__section">
      <h3 class="service-detail__section-title">接入概览</h3>
      <dl class="service-detail__grid">
        <div class="service-detail__item">
          <dt>入口节点</dt>
          <dd>{{ agentLabel(service.entry_agent_id) }}</dd>
        </div>
        <div class="service-detail__item">
          <dt>服务侧节点</dt>
          <dd>{{ agentLabel(service.service_agent_id) }}</dd>
        </div>
        <div class="service-detail__item">
          <dt>后端 URL</dt>
          <dd class="service-detail__inline-copy" data-testid="detail-backend-url">
            <code class="service-detail__mono">{{ service.backend_url || '—' }}</code>
            <CopyButton
              :value="service.backend_url"
              label="复制后端 URL"
              success-message="已复制后端 URL"
              size="xs"
            />
          </dd>
        </div>
        <div class="service-detail__item">
          <dt>路径</dt>
          <dd>{{ pathText(service.path) }}</dd>
        </div>
        <div class="service-detail__item">
          <dt>切换阶段</dt>
          <dd>
            <BaseBadge v-if="service.switch_phase" tone="warning" size="sm">{{ service.switch_phase }}</BaseBadge>
            <span v-else>无</span>
          </dd>
        </div>
        <div class="service-detail__item">
          <dt>未完成环节</dt>
          <dd :class="{ 'service-detail__pending-text': steps.length }">
            {{ steps.length ? steps.join('；') : '无' }}
          </dd>
        </div>
        <div class="service-detail__item">
          <dt>证书</dt>
          <dd class="service-detail__inline-meta">
            <BaseBadge :tone="certTone" size="sm">{{ certificate.label }}</BaseBadge>
            <span v-if="certificate.detail" class="service-detail__muted">：{{ certificate.detail }}</span>
          </dd>
        </div>
        <div class="service-detail__item">
          <dt>流量</dt>
          <dd>
            <span class="service-detail__mono">{{ traffic.text }}</span>
            <span v-if="traffic.detail" class="service-detail__muted"> · {{ traffic.detail }}</span>
          </dd>
        </div>
        <div v-if="service.apply" class="service-detail__item service-detail__item--wide">
          <dt>生效状态</dt>
          <dd class="service-detail__inline-meta">
            <BaseBadge v-if="service.apply.pending" tone="warning" size="sm">待生效</BaseBadge>
            <span v-if="applyDetailText">{{ applyDetailText }}</span>
          </dd>
        </div>
      </dl>
    </section>

    <section class="service-detail__section">
      <h3 class="service-detail__section-title">节点探测</h3>
      <p v-if="!probes.length" class="service-detail__hint">
        控制面没有探测结果。节点在线或规则存在都不代表接入完成。
      </p>
      <ul v-else class="service-detail__probe-list">
        <li
          v-for="(probe, index) in probes"
          :key="`${probe.agentId}-${index}`"
          class="service-detail__probe-row"
          data-testid="probe-row"
        >
          <div class="service-detail__probe-main">
            <span class="service-detail__probe-agent">{{ probe.agentId || '未知节点' }}</span>
            <BaseBadge v-if="probe.purpose" tone="neutral" subtone="secondary" size="sm" mono>
              {{ probe.purpose }}
            </BaseBadge>
            <BaseBadge :tone="probeTone(probe)" size="sm" mono>{{ probe.text }}</BaseBadge>
            <BaseBadge
              v-if="probe.verification"
              :tone="probe.verification === 'expired' ? 'warning' : 'neutral'"
              size="sm"
              mono
            >
              {{ probe.verification }}
            </BaseBadge>
          </div>
          <div v-if="probe.target || probe.checkedAt || probe.error" class="service-detail__probe-meta">
            <code v-if="probe.target" class="service-detail__mono">{{ probe.target }}</code>
            <span v-if="probe.checkedAt" class="service-detail__muted">
              <template v-if="probe.target"> · </template>{{ probe.checkedAt }}
            </span>
            <span v-if="probe.error" class="service-detail__probe-error">
              <template v-if="probe.target || probe.checkedAt"> · </template>{{ probe.error }}
            </span>
          </div>
        </li>
      </ul>
    </section>

    <section class="service-detail__section" data-testid="client-confirmation">
      <div class="service-detail__section-head">
        <h3 class="service-detail__section-title">客户端访问</h3>
        <BaseBadge :tone="service.client_confirmation?.confirmed ? 'success' : 'neutral'" size="sm" dot>
          {{ service.client_confirmation?.confirmed ? '已由用户确认' : '未知' }}
        </BaseBadge>
      </div>
      <p v-if="service.client_confirmation?.confirmed" class="service-detail__confirm-status">
        用户确认页面可打开
        <span v-if="service.client_confirmation.confirmed_at" class="service-detail__muted">
          · {{ service.client_confirmation.confirmed_at }}
        </span>
      </p>
      <p v-else class="service-detail__confirm-status">客户端访问：未知</p>
      <p class="service-detail__hint">用户确认与节点探测分开，不表示媒体播放通过。</p>
      <div class="service-detail__confirm-actions">
        <a
          v-if="openHref"
          class="btn btn-secondary btn-sm"
          :href="openHref"
          target="_blank"
          rel="noopener"
        >
          打开
        </a>
        <button
          v-if="!service.client_confirmation?.confirmed"
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="submitting"
          @click="$emit('confirm-client')"
        >
          我确认页面可打开
        </button>
      </div>
    </section>

    <section v-if="service.rules?.length" class="service-detail__section">
      <h3 class="service-detail__section-title">关联规则</h3>
      <ul class="service-detail__rule-list">
        <li
          v-for="rule in service.rules"
          :key="`${rule.agent_id}:${rule.id}`"
          class="service-detail__rule-row"
        >
          <div class="service-detail__rule-info">
            <code class="service-detail__mono">{{ rule.frontend_url || service.entry_url }}</code>
            <span v-if="ruleBackendLabels(rule).length" class="service-detail__muted">
              · {{ ruleBackendLabels(rule).join('，') }}
            </span>
          </div>
          <a class="btn btn-ghost btn-sm" :href="advancedRuleHref(rule)">高级规则</a>
        </li>
      </ul>
    </section>
  </section>
</template>

<script setup>
import { computed } from 'vue'
import BaseBadge from '../base/BaseBadge.vue'
import CopyButton from '../common/CopyButton.vue'
import {
  advancedRuleHref,
  certificateText,
  certificateTone,
  incompleteSteps,
  pathText,
  probeText,
  probeTone,
  ruleBackendLabels,
  safeHttpHref,
  serviceSummaryTone,
  serviceTrafficText,
} from '../../utils/accessService.js'

const props = defineProps({
  service: { type: Object, required: true },
  agents: { type: Array, default: () => [] },
  submitting: { type: Boolean, default: false },
  refreshing: { type: Boolean, default: false },
})

defineEmits(['back', 'refresh', 'retry', 'retire', 'abandon', 'edit', 'delete', 'confirm-client'])

const summaryTone = computed(() => serviceSummaryTone(props.service))
const steps = computed(() => incompleteSteps(props.service))
const certificate = computed(() => certificateText(props.service?.certificate))
const certTone = computed(() => certificateTone(props.service?.certificate))
const traffic = computed(() => serviceTrafficText(props.service?.traffic))
const probes = computed(() => (
  Array.isArray(props.service?.probes) ? props.service.probes : []
).map((probe) => probeText(probe)))
const openHref = computed(() => safeHttpHref(props.service?.entry_url))
const showRetry = computed(() => (
  props.service?.summary === '失败' || props.service?.summary === '待确认'
))
const applyDetailText = computed(() => {
  const apply = props.service?.apply || {}
  const parts = []
  if (!apply.pending) parts.push('控制面没有把生效标为待处理')
  if (apply.old_entry_may_receive) parts.push('旧入口仍可能接收新请求')
  if (apply.new_entry_applied) parts.push('新入口已应用')
  if (apply.listener_applied) parts.push('监听器已生效')
  if (apply.certificate_ready) parts.push('证书已就绪')
  return parts.join('；')
})

function agentLabel(id) {
  const agent = (props.agents || []).find((item) => String(item.id) === String(id))
  return agent?.name || id || '—'
}
</script>

<style scoped>
.service-detail {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  margin-bottom: var(--space-5);
  padding: var(--space-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  min-width: 0;
}

.service-detail__topbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding-bottom: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
}

.service-detail__top-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-detail__spin {
  animation: service-detail-spin 0.9s linear infinite;
}

@keyframes service-detail-spin {
  to {
    transform: rotate(360deg);
  }
}

.service-detail__hero {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}

.service-detail__hero-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.service-detail__title {
  margin: 0;
  font-size: var(--text-xl);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.25;
  overflow-wrap: anywhere;
}

.service-detail__badges {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-detail__endpoint {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2-5);
  padding: var(--space-3) var(--space-3-5, 0.875rem);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  min-width: 0;
}

.service-detail__endpoint-main {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  flex: 1 1 14rem;
  min-width: 0;
}

.service-detail__endpoint-label {
  font-size: var(--text-2xs);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
}

.service-detail__endpoint-url {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-detail__endpoint-actions {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
  flex-shrink: 0;
}

.service-detail__section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
  padding: var(--space-4);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  min-width: 0;
}

.service-detail__section-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.service-detail__section-title {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.service-detail__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2-5);
  margin: 0;
}

.service-detail__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  min-width: 0;
}

.service-detail__item--wide {
  grid-column: 1 / -1;
}

.service-detail__item dt {
  font-size: var(--text-2xs);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
}

.service-detail__item dd {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  line-height: 1.45;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-detail__inline-copy {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
  min-width: 0;
}

.service-detail__mono {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-detail__pending-text {
  color: var(--color-warning);
}

.service-detail__inline-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-detail__muted {
  color: var(--color-text-tertiary);
}

.service-detail__hint {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  line-height: 1.5;
}

.service-detail__confirm-status {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.service-detail__confirm-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.service-detail__probe-list,
.service-detail__rule-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.service-detail__probe-row {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  min-width: 0;
}

.service-detail__probe-main {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-detail__probe-agent {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.service-detail__probe-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.service-detail__probe-error {
  color: var(--color-danger);
}

.service-detail__rule-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  min-width: 0;
}

.service-detail__rule-info {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1);
  min-width: 0;
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

@media (max-width: 768px) {
  .service-detail__grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .service-detail {
    padding: var(--space-4);
  }

  .service-detail__section {
    padding: var(--space-3);
  }
}

@media (prefers-reduced-motion: reduce) {
  .service-detail__spin {
    animation: none;
  }
}
</style>
