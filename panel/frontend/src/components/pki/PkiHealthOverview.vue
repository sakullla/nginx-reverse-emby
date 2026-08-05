<template>
  <section class="pki-health" aria-label="内部 PKI 概览">
    <StatCard
      :tone="domainTone"
      :value="domainValue"
      label="PKI 域"
      sub-label="内部信任边界标识"
    >
      <template #icon>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
        </svg>
      </template>
    </StatCard>

    <StatCard
      tone="primary"
      :value="epochValue"
      label="Epoch / 安全修订"
      sub-label="安全快照版本"
    >
      <template #icon>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="23 4 23 10 17 10"/>
          <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
        </svg>
      </template>
    </StatCard>

    <StatCard
      tone="primary"
      :value="countsValue"
      label="身份 / 证书"
      sub-label="当前登记规模"
    >
      <template #icon>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
          <circle cx="9" cy="7" r="4"/>
          <path d="M23 21v-2a4 4 0 0 0-3-3.87"/>
          <path d="M16 3.13a4 4 0 0 1 0 7.75"/>
        </svg>
      </template>
    </StatCard>

    <StatCard
      :tone="runtimeTone"
      :value="runtimeLabel"
      label="运行状态"
      :sub-label="runtimeRaw"
    >
      <template #icon>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M22 12h-4l-3 9L9 3l-3 9H2"/>
        </svg>
      </template>
    </StatCard>
  </section>
</template>

<script setup>
import { computed } from 'vue'
import StatCard from '../base/StatCard.vue'

const props = defineProps({
  overview: { type: Object, default: () => ({}) },
  identityCount: { type: Number, default: 0 },
  certificateCount: { type: Number, default: 0 },
  runtimeStatusLabel: { type: Function, required: true },
})

const domainValue = computed(() => props.overview.pki_domain_id || '尚未初始化')
const domainTone = computed(() => (props.overview.pki_domain_id ? 'success' : 'warning'))
const epochValue = computed(() => `${props.overview.pki_epoch ?? '—'} / ${props.overview.security_revision ?? '—'}`)
const countsValue = computed(() => `${props.overview.identity_count ?? props.identityCount} / ${props.overview.certificate_count ?? props.certificateCount}`)
const runtimeRaw = computed(() => props.overview.runtime_status || 'unknown')
const runtimeLabel = computed(() => props.runtimeStatusLabel(props.overview.runtime_status))
const runtimeTone = computed(() => {
  const value = String(props.overview.runtime_status || '').toLowerCase()
  if (value === 'healthy') return 'success'
  if (value === 'degraded') return 'warning'
  if (value === 'unavailable') return 'danger'
  return 'primary'
})
</script>

<style scoped>
.pki-health {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
}

.pki-health :deep(.stat-card__value) {
  font-size: 1.15rem;
  overflow-wrap: anywhere;
  word-break: break-word;
}

@media (min-width: 1920px) {
  .pki-health {
    gap: var(--space-4);
  }

  .pki-health :deep(.stat-card) {
    padding: var(--space-6);
  }

  .pki-health :deep(.stat-card__value) {
    font-size: 1.35rem;
  }
}

@media (max-width: 1100px) {
  .pki-health {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .pki-health {
    grid-template-columns: 1fr 1fr;
    gap: var(--space-2);
  }

  .pki-health :deep(.stat-card) {
    padding: var(--space-3);
  }

  .pki-health :deep(.stat-card__icon) {
    width: 2rem;
    height: 2rem;
    margin-bottom: var(--space-2);
  }

  .pki-health :deep(.stat-card__value) {
    font-size: 0.95rem;
  }

  .pki-health :deep(.stat-card__label),
  .pki-health :deep(.stat-card__sub-label) {
    font-size: 0.7rem;
  }
}

@media (max-width: 380px) {
  .pki-health {
    grid-template-columns: 1fr;
  }
}
</style>
