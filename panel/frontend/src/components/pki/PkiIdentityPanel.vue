<template>
  <PkiSection
    title="端点身份与证书"
    description="活动有效凭据优先；强制换证与撤销需明确确认。"
    eyebrow="处置"
    aria-label="端点身份与证书"
    collapsible
    storage-key="nre.pki.section.identity"
  >
    <div class="identity-list">
      <BaseListCard
        v-for="row in identities"
        :key="row.id"
        data-test="identity-row"
        :status="row.revoked ? 'danger' : (row.canRotate ? 'success' : 'warning')"
        :clickable="false"
        class="identity-card"
      >
        <template #header-left>
          <BaseBadge tone="neutral" subtone="secondary" mono>{{ shortId(row.id) }}</BaseBadge>
          <strong class="identity-card__id mono" :title="row.id">{{ row.id }}</strong>
          <PkiStatusBadge
            :status="row.revoked ? 'revoked' : (row.rotationPhase === 'idle' || !row.rotationPhase ? 'active' : row.rotationPhase)"
            :label="row.revoked ? '已撤销' : (row.rotationPhase && row.rotationPhase !== 'idle' ? row.rotationPhase : '活动')"
            dot
          />
        </template>
        <div class="identity-card__owner">{{ row.owner }}</div>

        <div class="identity-card__grid">
          <div class="identity-field">
            <span class="identity-field__label">用途 / 链</span>
            <strong>{{ row.purpose }}</strong>
            <span v-if="purposeLabel(row.purpose) !== row.purpose">{{ purposeLabel(row.purpose) }}</span>
            <span>CA generation {{ row.caGeneration }}</span>
          </div>
          <div class="identity-field">
            <span class="identity-field__label">序列号 / 指纹</span>
            <span class="mono">{{ row.serial }}</span>
            <span class="mono identity-field__fingerprint" :title="row.fingerprint">{{ row.fingerprint }}</span>
          </div>
          <div class="identity-field">
            <span class="identity-field__label">有效期 / 下一步</span>
            <span>{{ formatDate(row.notBefore) }} → {{ formatDate(row.notAfter) }}</span>
            <span>{{ row.nextAction }}</span>
          </div>
          <div class="identity-field">
            <span class="identity-field__label">轮转 / 撤销 / 错误</span>
            <span>{{ row.rotationPhase || '—' }}</span>
            <span :class="{ 'danger-text': row.revoked }">{{ row.revocation }}</span>
            <span v-if="row.latestError" class="danger-text">{{ row.latestError }}</span>
          </div>
        </div>

        <template #footer>
          <div class="identity-card__actions">
            <button
              type="button"
              class="btn btn--secondary btn--sm"
              data-test="identity-force-rotate"
              :disabled="!row.canRotate"
              @click="emit('force-rotate', row)"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <polyline points="23 4 23 10 17 10"/>
                <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
              </svg>
              强制换证
            </button>
            <button
              type="button"
              class="btn btn--danger-soft btn--sm"
              data-test="identity-revoke"
              :disabled="!row.canRevoke"
              @click="emit('revoke', row)"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <circle cx="12" cy="12" r="10"/>
                <line x1="4.93" y1="4.93" x2="19.07" y2="19.07"/>
              </svg>
              撤销
            </button>
          </div>
        </template>
      </BaseListCard>

      <div v-if="!identities.length" class="pki-empty">
        <div class="pki-empty__icon" aria-hidden="true">∅</div>
        <p>暂无内部 PKI 身份。</p>
      </div>
    </div>

    <ListPagination
      v-if="total > pageSize"
      data-test="identity-pagination"
      :page="page"
      :page-size="pageSize"
      :total="total"
      @update:page="$emit('update:page', $event)"
    />
  </PkiSection>
</template>

<script setup>
import BaseBadge from '../base/BaseBadge.vue'
import BaseListCard from '../base/BaseListCard.vue'
import ListPagination from '../common/ListPagination.vue'
import PkiSection from './PkiSection.vue'
import PkiStatusBadge from './PkiStatusBadge.vue'

defineProps({
  identities: { type: Array, default: () => [] },
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 5 },
  total: { type: Number, default: 0 },
  purposeLabel: { type: Function, required: true },
  formatDate: { type: Function, required: true },
})

const emit = defineEmits(['force-rotate', 'revoke', 'update:page'])

function shortId(id) {
  const value = String(id || '')
  if (value.length <= 12) return value
  return `${value.slice(0, 6)}…${value.slice(-4)}`
}
</script>

<style scoped>
.identity-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.identity-card :deep(.base-list-card__header-left) {
  min-width: 0;
}

.identity-card__id {
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: min(420px, 48vw);
}

.identity-card__owner {
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  margin-bottom: 0.15rem;
}

.identity-card__actions {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  flex-wrap: wrap;
}

.identity-card__actions .btn {
  min-height: 1.9rem;
  padding: 0.3rem 0.65rem;
  font-size: 0.72rem;
  gap: 0.28rem;
}

@media (max-width: 640px) {
  .identity-list {
    grid-template-columns: 1fr !important;
  }

  .identity-card :deep(.base-list-card__header) {
    flex-direction: column;
    align-items: stretch;
    gap: 0.55rem;
  }

  .identity-card :deep(.base-list-card__header-right) {
    width: 100%;
  }

  .identity-card__actions {
    width: 100%;
  }

  .identity-card__actions .btn {
    flex: 1 1 calc(50% - 0.2rem);
    min-height: 2.25rem;
    justify-content: center;
  }

  .identity-card__id {
    max-width: 100%;
    white-space: normal;
    word-break: break-all;
  }

  .identity-card__grid {
    grid-template-columns: 1fr;
  }
}

.identity-card__grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
}

.identity-field {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  min-width: 0;
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

.identity-field strong {
  color: var(--color-text-primary);
  font-size: var(--text-sm);
}

.identity-field__label {
  color: var(--color-text-tertiary);
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  font-size: 0.65rem;
}

.identity-field__fingerprint {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pki-empty {
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
  color: var(--color-text-tertiary);
  font-weight: 700;
}

.pki-empty p {
  margin: 0;
  font-size: var(--text-sm);
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.danger-text {
  color: var(--color-danger) !important;
}

@media (min-width: 1920px) {
  .identity-list {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-4);
  }

  .identity-list > .pki-empty {
    grid-column: 1 / -1;
  }
}

@media (min-width: 2560px) {
  .identity-list {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 1000px) {
  .identity-card__grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .identity-card__grid {
    grid-template-columns: 1fr;
  }
}
</style>
