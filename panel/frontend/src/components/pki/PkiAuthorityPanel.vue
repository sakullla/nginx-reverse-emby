<template>
  <PkiSection
    title="内部信任根"
    description="当前用来给节点签发证书的根。换根请用上方「日常 / 紧急」轮转。"
    eyebrow="信任根"
    aria-label="CA generations"
    collapsible
    storage-key="nre.pki.section.authority"
  >
    <div class="authority-catalog">
      <BaseListCard
        v-for="authority in authorities"
        :key="authority.id"
        data-test="authority-row"
        :clickable="false"
        class="authority-card"
        :status="authorityStatusTone(authority.status)"
      >
        <template #header-left>
          <span class="authority-card__title">第 {{ authority.generation }} 代</span>
          <PkiStatusBadge :status="authority.status" :label="authorityStatusLabel(authority.status)" />
        </template>
        <p class="authority-card__hint">
          {{ authority.status === 'active' ? '正在给节点签发证书' : '已不再作为主信任根' }}
        </p>
        <template #footer>
          <span class="authority-card__meta">有效至 {{ formatDay(authority.not_after) }}</span>
        </template>
      </BaseListCard>

      <div v-if="!authorities.length" class="pki-empty">
        <div class="pki-empty__icon" aria-hidden="true">∅</div>
        <p>还没有内部信任根。</p>
      </div>
    </div>
  </PkiSection>
</template>

<script setup>
import BaseListCard from '../base/BaseListCard.vue'
import PkiSection from './PkiSection.vue'
import PkiStatusBadge from './PkiStatusBadge.vue'

const props = defineProps({
  authorities: { type: Array, default: () => [] },
  authorityStatusLabel: { type: Function, required: true },
  formatDate: { type: Function, required: true },
})

function authorityStatusTone(status) {
  const value = String(status || '').toLowerCase()
  if (value === 'active') return 'success'
  if (value === 'revoked') return 'danger'
  if (['retiring', 'prepared'].includes(value)) return 'warning'
  return 'neutral'
}

function formatDay(value) {
  if (!value) return '—'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return props.formatDate(value)
  return parsed.toLocaleDateString('zh-CN')
}
</script>

<style scoped>
.authority-catalog {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(16.5rem, 1fr));
  gap: 0.75rem;
  padding: 4px;
  margin: -4px;
}

.authority-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.authority-card :deep(.base-list-card__footer) {
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}

.authority-card__title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.authority-card__hint {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.authority-card__meta {
  color: var(--color-text-secondary);
  font-size: 0.75rem;
}

.pki-empty {
  grid-column: 1 / -1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-8) var(--space-4);
  color: var(--color-text-tertiary);
  text-align: center;
}

.pki-empty__icon {
  width: 2rem;
  height: 2rem;
  border-radius: var(--radius-full);
  display: grid;
  place-items: center;
  background: var(--color-bg-subtle);
  font-weight: 700;
}

.pki-empty p {
  margin: 0;
  font-size: var(--text-sm);
}

@media (max-width: 640px) {
  .authority-catalog {
    grid-template-columns: 1fr;
  }
}
</style>
