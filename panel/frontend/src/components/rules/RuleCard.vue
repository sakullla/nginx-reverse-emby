<template>
  <BaseListCard
    :status="statusTone"
    :disabled="!rule.enabled"
    @click="$emit('edit', rule)"
  >
    <template #header-left>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ rule.id }}</BaseBadge>
      <BaseBadge :tone="isHttps ? 'success' : 'primary'" shape="square" mono>
        {{ isHttps ? 'HTTPS' : 'HTTP' }}
      </BaseBadge>
      <BaseBadge :tone="statusTone" dot>{{ statusLabel }}</BaseBadge>
    </template>
    <template #header-right>
      <BaseIconButton tone="warning" :title="rule.enabled ? '停用' : '启用'" @click="$emit('toggle', rule)">
        <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <rect x="6" y="4" width="4" height="16" rx="1"/>
          <rect x="14" y="4" width="4" height="16" rx="1"/>
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
          <polygon points="5 3 19 12 5 21 5 3"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="复制" @click="$emit('copy', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="9" y="9" width="13" height="13" rx="2"/>
          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="编辑" @click="$emit('edit', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton tone="primary" title="诊断" @click="$emit('diagnose', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M3 12h4l2-6 4 12 2-6h6"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton tone="danger" title="删除" @click="$emit('delete', rule)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="rule-card__mapping">
      <div class="rule-card__url-row">
        <span class="rule-card__url-icon">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
          </svg>
        </span>
        <code class="rule-card__url">{{ rule.frontend_url }}</code>
      </div>
      <div class="rule-card__url-row">
        <span class="rule-card__url-icon">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M5 12h14"/>
            <path d="M12 5l7 7-7 7"/>
          </svg>
        </span>
        <code class="rule-card__backend" :title="backendsTooltip">{{ backendLabel }}</code>
      </div>
    </div>

    <div v-if="hasMetaIndicators" class="rule-card__meta">
      <BaseBadge v-if="hasUserAgent" tone="neutral" subtone="secondary">UA</BaseBadge>
      <BaseBadge v-if="hasCustomHeaders" tone="neutral" subtone="secondary">Headers</BaseBadge>
      <BaseBadge v-if="hasIpForwardDisabled" tone="warning">No IP Forward</BaseBadge>
    </div>

    <div class="traffic-line">
      <span>↓ {{ formatBytes(normalizedTraffic.rx_bytes) }}</span>
      <span>↑ {{ formatBytes(normalizedTraffic.tx_bytes) }}</span>
    </div>

    <template v-if="hasTags" #footer>
      <BaseBadge v-for="tag in rule.tags" :key="tag" tone="primary">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import { getRuleEffectiveStatus } from '../../utils/syncStatus.js'
import { formatBytes, normalizeTrafficBucket } from '../../utils/trafficStats.js'

const props = defineProps({
  rule: { type: Object, required: true },
  agent: { type: Object, default: null },
  traffic: { type: Object, default: () => ({ rx_bytes: 0, tx_bytes: 0 }) },
})

defineEmits(['edit', 'toggle', 'copy', 'diagnose', 'delete'])

const STATUS_TONE = {
  active: 'success',
  pending: 'warning',
  failed: 'danger',
  disabled: 'neutral',
}

const STATUS_LABEL = {
  active: '生效中',
  pending: '待同步',
  failed: '同步失败',
  disabled: '已禁用',
}

const status = computed(() => getRuleEffectiveStatus(props.rule, props.agent))
const statusTone = computed(() => STATUS_TONE[status.value] || 'neutral')
const statusLabel = computed(() => STATUS_LABEL[status.value] || '未知')

const isHttps = computed(() => String(props.rule.frontend_url || '').startsWith('https'))

const backends = computed(() => {
  if (Array.isArray(props.rule.backends) && props.rule.backends.length > 0) {
    return props.rule.backends
      .map((b) => String(b?.url || '').trim())
      .filter(Boolean)
  }
  return props.rule.backend_url ? [String(props.rule.backend_url).trim()] : []
})

const backendLabel = computed(() => {
  const list = backends.value
  if (list.length === 0) return '-'
  if (list.length === 1) return list[0]
  return `${list[0]} +${list.length - 1}`
})
const backendsTooltip = computed(() => backends.value.join('\n'))

const hasUserAgent = computed(() => Boolean(String(props.rule?.user_agent || '').trim()))
const hasCustomHeaders = computed(() =>
  Array.isArray(props.rule?.custom_headers) && props.rule.custom_headers.length > 0
)
const hasIpForwardDisabled = computed(() => props.rule?.pass_proxy_headers === false)
const hasMetaIndicators = computed(
  () => hasUserAgent.value || hasCustomHeaders.value || hasIpForwardDisabled.value
)
const normalizedTraffic = computed(() => normalizeTrafficBucket(props.traffic))
const hasTags = computed(() => Array.isArray(props.rule.tags) && props.rule.tags.length > 0)
</script>

<style scoped>
.rule-card__mapping {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}
.rule-card__url-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
}
.rule-card__url-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}
.rule-card__url,
.rule-card__backend {
  font-family: var(--font-mono);
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
}
.rule-card__meta {
  display: flex;
  gap: 0.375rem;
  flex-wrap: wrap;
}
.traffic-line {
  display: flex;
  gap: 0.75rem;
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
  font-variant-numeric: tabular-nums;
}
</style>
