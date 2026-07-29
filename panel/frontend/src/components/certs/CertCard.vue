<template>
  <BaseListCard
    :status="statusTone"
    :disabled="!cert.enabled"
    @click="$emit('edit', cert)"
  >
    <template #header-left>
      <BaseBadge tone="neutral" subtone="secondary" mono>#{{ cert.id }}</BaseBadge>
      <span
        v-if="cert.domain"
        class="cert-card__name"
        :title="cert.domain"
      >{{ cert.domain }}</span>
      <BaseBadge
        class="cert-card__scope"
        tone="neutral"
        subtone="secondary"
        shape="square"
        mono
      >
        {{ scopeLabel }}
      </BaseBadge>
      <BaseBadge :tone="badgeTone" dot>{{ statusLabel }}</BaseBadge>
      <!-- 已按节点筛选时，节点徽章重复；仅全部节点视图展示 -->
      <AgentBadge v-if="showAgentBadge" :item="cert" :agent="agent" />
    </template>
    <template #header-right>
      <BaseIconButton
        v-if="cert.status === 'issuing'"
        tone="default"
        title="签发中"
        disabled
      >
        <svg class="cert-card__spin" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M21 12a9 9 0 1 1-6.219-8.56"/>
        </svg>
      </BaseIconButton>
      <BaseIconButton
        v-else-if="cert.status === 'pending' || cert.status === 'error'"
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
      <BaseIconButton v-if="canDelete" tone="danger" title="删除" @click="$emit('delete', cert)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
        </svg>
      </BaseIconButton>
    </template>

    <div v-if="metaChips.length || formattedDate" class="cert-card__meta">
      <BaseBadge
        v-for="chip in metaChips"
        :key="chip.key"
        :tone="chip.tone"
        :subtone="chip.subtone"
        size="sm"
        :shape="chip.shape || 'pill'"
        :mono="!!chip.mono"
        :title="chip.title || undefined"
      >
        {{ chip.label }}
      </BaseBadge>
      <span v-if="formattedDate" class="cert-card__date" :title="`签发时间 ${formattedDate}`">{{ formattedDate }}</span>
    </div>

    <div v-if="expiry" class="cert-card__expiry">
      <BaseBadge :tone="expiry.tone" size="sm">{{ expiry.remainingLabel }}</BaseBadge>
      <span class="cert-card__expiry-date" :title="`到期时间 ${expiry.dateLabel}`">到期 {{ expiry.dateLabel }}</span>
    </div>

    <p v-if="cert.last_error" class="cert-card__error">
      <span class="cert-card__error-reason">{{ cert.last_error }}</span>
      <span v-if="nextRetryLabel" class="cert-card__retry">· {{ nextRetryLabel }}</span>
    </p>

    <template v-if="hasFooter" #footer>
      <BaseBadge v-if="isSystemRelayCA(cert)" tone="primary">系统 Relay CA</BaseBadge>
      <BaseBadge v-if="cert.self_signed" tone="warning">自签</BaseBadge>
      <BaseBadge v-for="tag in visibleTags" :key="tag" tone="primary">{{ tag }}</BaseBadge>
    </template>
  </BaseListCard>
</template>

<script setup>
import { computed } from 'vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseBadge from '../base/BaseBadge.vue'
import AgentBadge from '../common/AgentBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import {
  getCertificateSourceLabel,
  getCertificateUsageLabel,
  isSystemManagedRelayListenerCertificate,
  isSystemRelayCA,
} from '../../utils/certificateTemplates'
import { certCardStatusLabel, certCardStatusTone } from '../../utils/resourceCardStatus.js'
import { certExpiryInfo } from '../../utils/certExpiry.js'

const props = defineProps({
  cert: { type: Object, required: true },
  agent: { type: Object, default: null },
})

defineEmits(['edit', 'delete', 'issue'])

// agent prop is the page-selected node; when set, every card would repeat the same badge.
const showAgentBadge = computed(() => !props.agent)

/** Badge may use primary for issuing visual; card strip uses four-tone map. */
const BADGE_TONE = {
  active: 'success',
  pending: 'warning',
  issuing: 'primary',
  error: 'danger',
}

const statusTone = computed(() => certCardStatusTone(props.cert))
const statusLabel = computed(() => certCardStatusLabel(props.cert))
const badgeTone = computed(() => {
  if (!props.cert.enabled) return 'neutral'
  return BADGE_TONE[props.cert.status] || 'neutral'
})

const scopeLabel = computed(() => (props.cert.scope === 'ip' ? 'IP' : '域名'))

function formatUnixSeconds(unix) {
  if (!unix || unix <= 0) return ''
  try {
    return new Date(unix * 1000).toLocaleString('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return ''
  }
}

const nextRetryLabel = computed(() => {
  const ts = props.cert.next_retry_at_unix
  if (!ts || ts <= 0) return ''
  const formatted = formatUnixSeconds(ts)
  if (!formatted) return ''
  const retryCount = Number(props.cert.retry_count) || 0
  const countPart = retryCount > 0 ? `（第 ${retryCount} 次）` : ''
  return `下次重试 ${formatted}${countPart}`
})

const usageChip = computed(() => {
  const usage = props.cert?.usage
  if (usage === 'https') {
    return {
      key: 'usage-https',
      label: 'HTTPS',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: getCertificateUsageLabel(usage)
    }
  }
  if (usage === 'relay_tunnel') {
    return {
      key: 'usage-relay',
      label: 'Relay',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: getCertificateUsageLabel(usage)
    }
  }
  if (usage === 'relay_ca') {
    return {
      key: 'usage-relay-ca',
      label: 'Relay CA',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: getCertificateUsageLabel(usage)
    }
  }
  if (usage === 'mixed') {
    return {
      key: 'usage-mixed',
      label: '混合',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: getCertificateUsageLabel(usage)
    }
  }
  const full = getCertificateUsageLabel(usage)
  return {
    key: 'usage-other',
    label: full,
    tone: 'neutral',
    subtone: 'secondary',
    shape: 'square',
    mono: true,
    title: full
  }
})

const issuerChip = computed(() => {
  if (isSystemManagedRelayListenerCertificate(props.cert)) {
    return {
      key: 'issuer-system',
      label: '系统签发',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: '系统自动签发'
    }
  }
  const full = getCertificateSourceLabel(props.cert?.certificate_type)
  if (props.cert?.certificate_type === 'acme') {
    return {
      key: 'issuer-acme',
      label: '自动签发',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: full
    }
  }
  if (props.cert?.certificate_type === 'uploaded') {
    return {
      key: 'issuer-upload',
      label: '手动上传',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: full
    }
  }
  if (props.cert?.certificate_type === 'internal_ca') {
    return {
      key: 'issuer-internal',
      label: '内部自签',
      tone: 'neutral',
      subtone: 'secondary',
      shape: 'square',
      mono: true,
      title: full
    }
  }
  return {
    key: 'issuer-other',
    label: full,
    tone: 'neutral',
    subtone: 'secondary',
    shape: 'square',
    mono: true,
    title: full
  }
})

const metaChips = computed(() => [usageChip.value, issuerChip.value].filter(Boolean))

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

const expiry = computed(() => certExpiryInfo(props.cert.not_after))

const visibleTags = computed(() => {
  const tags = Array.isArray(props.cert.tags) ? props.cert.tags : []
  return tags.filter((tag) => !String(tag || '').startsWith('system:'))
})

const hasFooter = computed(() =>
  isSystemRelayCA(props.cert) ||
  props.cert.self_signed ||
  visibleTags.value.length > 0
)

const canDelete = computed(() => !isSystemRelayCA(props.cert))
</script>

<style scoped>
.cert-card__name {
  min-width: 0;
  max-width: min(18rem, 100%);
  margin-right: 0.1rem;
  font-family: var(--font-mono);
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  line-height: 1.2;
  letter-spacing: -0.025em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cert-card__scope {
  flex-shrink: 0;
  letter-spacing: 0.02em;
}

.cert-card__meta {
  display: flex;
  align-items: center;
  gap: 0.28rem;
  flex-wrap: wrap;
  min-width: 0;
}

.cert-card__date {
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  margin-left: auto;
  font-variant-numeric: tabular-nums;
  line-height: 1.3;
  white-space: nowrap;
}

.cert-card__expiry {
  display: flex;
  align-items: center;
  gap: 0.28rem;
  flex-wrap: wrap;
  min-width: 0;
}

.cert-card__expiry-date {
  font-size: 0.6875rem;
  color: var(--color-text-muted);
  margin-left: auto;
  font-variant-numeric: tabular-nums;
  line-height: 1.3;
  white-space: nowrap;
}

.cert-card__error {
  font-size: 0.6875rem;
  color: var(--color-danger);
  background: var(--color-danger-50);
  padding: 0.25rem 0.45rem;
  border-radius: var(--radius-sm);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.35;
}

.cert-card__retry {
  margin-left: 0.25rem;
  color: var(--color-text-tertiary);
}

.cert-card__spin {
  animation: cert-card-spin 0.9s linear infinite;
}

@keyframes cert-card-spin {
  to { transform: rotate(360deg); }
}

@media (max-width: 640px) {
  :deep(.base-icon-button) {
    width: 36px;
    height: 36px;
  }

  .cert-card__name {
    font-size: 0.875rem;
  }
}
</style>
