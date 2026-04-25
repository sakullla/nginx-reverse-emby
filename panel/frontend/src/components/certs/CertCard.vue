<template>
  <BaseListCard
    :status="certTone"
    :disabled="!cert.enabled"
    :title="cert.domain"
    @click="$emit('edit', cert)"
  >
    <template #header-left>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ cert.id }}</BaseBadge>
      <BaseBadge tone="neutral" subtone="secondary" shape="square" mono>
        {{ cert.scope === 'ip' ? 'IP' : '域名' }}
      </BaseBadge>
      <BaseBadge :tone="certTone" dot>{{ statusLabel }}</BaseBadge>
    </template>
    <template #header-right>
      <BaseIconButton
        v-if="cert.status === 'pending' || cert.status === 'error'"
        tone="success"
        title="签发"
        @click="$emit('issue', cert)"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton title="编辑" @click="$emit('edit', cert)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton
        v-if="!isSystemRelayCA(cert)"
        tone="danger"
        title="删除"
        @click="$emit('delete', cert)"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
        </svg>
      </BaseIconButton>
    </template>

    <div class="cert-card__meta">
      <BaseBadge tone="neutral" subtone="secondary" shape="square" mono>
        {{ getCertificateUsageLabel(cert.usage) }}
      </BaseBadge>
      <BaseBadge tone="neutral" subtone="secondary" shape="square" mono>
        {{ issuerLabel }}
      </BaseBadge>
      <span v-if="cert.last_issue_at" class="cert-card__date">{{ formattedDate }}</span>
    </div>

    <p v-if="cert.last_error" class="cert-card__error">{{ cert.last_error }}</p>

    <template v-if="hasFooter" #footer>
      <BaseBadge v-if="isSystemRelayCA(cert)" tone="primary">系统 Relay CA</BaseBadge>
      <BaseBadge v-if="cert.self_signed" tone="warning">自签</BaseBadge>
      <BaseBadge v-for="tag in cert.tags || []" :key="tag" tone="primary">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import {
  getCertificateSourceLabel,
  getCertificateUsageLabel,
  isSystemManagedRelayListenerCertificate,
  isSystemRelayCA,
} from '../../utils/certificateTemplates'

const props = defineProps({
  cert: { type: Object, required: true },
})

defineEmits(['edit', 'delete', 'issue'])

const STATUS_TONE = {
  active: 'success',
  pending: 'warning',
  error: 'danger',
}

const certTone = computed(() => {
  if (!props.cert.enabled) return 'neutral'
  return STATUS_TONE[props.cert.status] || 'neutral'
})

const statusLabel = computed(() => {
  if (!props.cert.enabled) return '已禁用'
  if (props.cert.status === 'active') return '生效中'
  if (props.cert.status === 'pending') return '待签发'
  if (props.cert.status === 'error') return '签发失败'
  return '未知'
})

const issuerLabel = computed(() => {
  if (isSystemManagedRelayListenerCertificate(props.cert)) return '系统自动签发'
  return getCertificateSourceLabel(props.cert?.certificate_type)
})

const formattedDate = computed(() => {
  const dateStr = props.cert.last_issue_at
  if (!dateStr) return ''
  try {
    return new Date(dateStr).toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return dateStr
  }
})

const hasFooter = computed(() =>
  isSystemRelayCA(props.cert) ||
  props.cert.self_signed ||
  (Array.isArray(props.cert.tags) && props.cert.tags.length > 0)
)
</script>

<style scoped>
.cert-card__meta {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.cert-card__date {
  font-size: 0.75rem;
  color: var(--color-text-muted);
  margin-left: auto;
}
.cert-card__error {
  font-size: 0.75rem;
  color: var(--color-danger);
  background: var(--color-danger-50);
  padding: 0.25rem 0.5rem;
  border-radius: var(--radius-sm);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
