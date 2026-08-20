<template>
  <PkiSection
    title="节点证书"
    description="需要换证或停用时，点卡片查看说明，再用按钮确认。"
    eyebrow="处置"
    aria-label="端点身份与证书"
    collapsible
    storage-key="nre.pki.section.identity"
  >
    <template #actions>
      <div class="search-wrapper identity-search" @click="focusSearch">
        <svg class="search-icon-btn" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="8" />
          <line x1="21" y1="21" x2="16.65" y2="16.65" />
        </svg>
        <input
          ref="searchInputRef"
          :value="query"
          class="search-input"
          type="search"
          placeholder="搜索节点或监听器"
          aria-label="搜索端点身份"
          @input="emit('update:query', $event.target.value)"
          @keydown.esc.prevent="emit('update:query', '')"
        >
        <button
          v-if="query.trim()"
          type="button"
          class="clear-btn"
          aria-label="清空搜索"
          @click.stop="emit('update:query', '')"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>
    </template>

    <div class="identity-catalog">
      <BaseListCard
        v-for="row in identities"
        :key="row.id"
        data-test="identity-row"
        :status="row.revoked ? 'danger' : (row.canRotate ? 'success' : 'warning')"
        clickable
        class="identity-card"
        @click="inspect(row)"
      >
        <template #header-left>
          <span class="identity-card__title" :title="row.ownerTitle">{{ row.ownerTitle || row.owner || '—' }}</span>
          <PkiStatusBadge
            :status="identityBadge(row).status"
            :label="identityBadge(row).label"
            dot
          />
        </template>
        <template #header-right>
          <BaseBadge tone="neutral" subtone="secondary">{{ row.ownerKind || '节点' }}</BaseBadge>
        </template>

        <p v-if="readablePurpose(row)" class="identity-card__purpose">{{ readablePurpose(row) }}</p>
        <p class="identity-card__meta">
          <span>{{ row.revoked ? '已停用' : (row.notAfter ? `有效至 ${row.notAfterLabel || formatDate(row.notAfter)}` : '还没有证书') }}</span>
          <span v-if="row.nextActionLabel && row.nextActionLabel !== '无需处理'">{{ row.nextActionLabel }}</span>
        </p>

        <template #footer>
          <div class="identity-card__footer" @click.stop>
            <span
              class="identity-card__revocation"
              :class="{ 'danger-text': row.revoked }"
              :title="row.revoked ? identityFooter(row) : ''"
            >{{ row.revoked ? identityFooter(row) : '' }}</span>
            <div class="identity-card__actions">
              <button
                type="button"
                class="btn btn--secondary btn--sm"
                data-test="identity-force-rotate"
                :disabled="!row.canRotate"
                @click="emit('force-rotate', row)"
              >
                换证
              </button>
              <button
                type="button"
                class="btn btn--danger-soft btn--sm"
                data-test="identity-revoke"
                :disabled="!row.canRevoke"
                @click="emit('revoke', row)"
              >
                撤销
              </button>
            </div>
          </div>
        </template>
      </BaseListCard>

      <div v-if="!identities.length" class="pki-empty">
        <div class="pki-empty__icon" aria-hidden="true">∅</div>
        <p>{{ query.trim() ? '没有匹配的节点证书。' : '还没有节点证书。' }}</p>
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

    <BaseModal
      :model-value="Boolean(inspected)"
      :title="inspected?.ownerTitle || '节点证书'"
      :subtitle="inspected ? `${inspected.ownerKind || '节点'} · ${identityBadge(inspected).label}` : ''"
      data-test="identity-inspect"
      show-footer
      @update:model-value="onInspectChange"
    >
      <div v-if="inspected" class="identity-inspect">
        <p class="identity-inspect__lede">
          {{ purposeLabel(inspected.purpose) }}
          ·
          {{ inspected.revoked ? '已停用' : `有效至 ${inspected.notAfterLabel || formatDate(inspected.notAfter)}` }}
        </p>
        <p class="identity-inspect__next">{{ inspected.nextActionLabel || '无需处理' }}</p>
        <p v-if="inspected.latestError" class="danger-text">{{ inspected.latestError }}</p>
        <details class="identity-inspect__tech">
          <summary>技术详情</summary>
          <dl>
            <div>
              <dt>用途代码</dt>
              <dd class="mono">{{ inspected.purpose }}</dd>
            </div>
            <div>
              <dt>信任根代数</dt>
              <dd>CA generation {{ inspected.caGeneration }}</dd>
            </div>
            <div>
              <dt>序列号</dt>
              <dd class="mono">{{ inspected.serial }}</dd>
            </div>
            <div>
              <dt>指纹</dt>
              <dd class="mono identity-inspect__wrap">{{ inspected.fingerprint }}</dd>
            </div>
            <div>
              <dt>有效期</dt>
              <dd>{{ formatDate(inspected.notBefore) }} → {{ formatDate(inspected.notAfter) }}</dd>
            </div>
            <div>
              <dt>原始下一步</dt>
              <dd>{{ inspected.nextAction }}</dd>
            </div>
          </dl>
        </details>
      </div>
      <template #footer>
        <button class="btn btn--secondary" type="button" @click="inspected = null">关闭</button>
        <button
          type="button"
          class="btn btn--secondary"
          :disabled="!inspected?.canRotate"
          @click="runInspectAction('force-rotate')"
        >换证</button>
        <button
          type="button"
          class="btn btn--danger"
          :disabled="!inspected?.canRevoke"
          @click="runInspectAction('revoke')"
        >撤销</button>
      </template>
    </BaseModal>
  </PkiSection>
</template>

<script setup>
import { ref } from 'vue'
import BaseBadge from '../base/BaseBadge.vue'
import BaseListCard from '../base/BaseListCard.vue'
import BaseModal from '../base/BaseModal.vue'
import ListPagination from '../common/ListPagination.vue'
import PkiSection from './PkiSection.vue'
import PkiStatusBadge from './PkiStatusBadge.vue'

const props = defineProps({
  identities: { type: Array, default: () => [] },
  query: { type: String, default: '' },
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 5 },
  total: { type: Number, default: 0 },
  purposeLabel: { type: Function, required: true },
  formatDate: { type: Function, required: true },
})

const emit = defineEmits(['force-rotate', 'revoke', 'update:page', 'update:query'])

const searchInputRef = ref(null)
const inspected = ref(null)

function stateLabel(value) {
  const raw = String(value || '').trim()
  return ({
    active: '正常',
    revoked: '已撤销',
    enrollment_required: '尚未登记',
    pending: '等待中',
    superseded: '已替换',
    expired: '已过期',
    prepared: '已准备',
  })[raw.toLowerCase()] || raw || '—'
}

function rotationLabel(value) {
  const raw = String(value || '').trim()
  if (!raw || raw === '—' || raw === 'idle') return ''
  return ({
    renewing: '续签中',
    rotating: '换证中',
    overlapping: '新旧并存',
  })[raw.toLowerCase()] || raw
}

function identityBadge(row = {}) {
  if (row.revoked) return { status: 'revoked', label: '已撤销' }
  const phase = rotationLabel(row.rotationPhase)
  if (phase) return { status: row.rotationPhase, label: phase }
  if (row.canRotate) return { status: 'active', label: '正常' }
  return { status: 'warning', label: stateLabel(row.revocation) }
}

function identityFooter(row = {}) {
  if (row.revoked) return row.revocation || '已撤销'
  return stateLabel(row.revocation)
}

function readablePurpose(row = {}) {
  const label = props.purposeLabel(row.purpose)
  if (!label || label === '—' || label === '-') return ''
  return label
}

function focusSearch() {
  searchInputRef.value?.focus?.()
}

function inspect(row) {
  inspected.value = row
}

function onInspectChange(open) {
  if (!open) inspected.value = null
}

function runInspectAction(kind) {
  const row = inspected.value
  if (!row) return
  inspected.value = null
  emit(kind, row)
}
</script>

<style scoped>
.identity-search {
  flex: 1 1 12rem;
  min-width: 0;
  max-width: 22rem;
}

.identity-search :deep(.search-input),
.identity-search .search-input {
  width: 100%;
  min-width: 0;
}

.identity-search .search-input::-webkit-search-decoration,
.identity-search .search-input::-webkit-search-cancel-button {
  appearance: none;
}

.identity-catalog {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(17.5rem, 1fr));
  gap: 0.75rem;
  padding: 4px;
  margin: -4px;
}

.identity-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.identity-card :deep(.base-list-card__footer) {
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}

.identity-card__title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.identity-card__purpose,
.identity-card__meta {
  margin: 0;
  min-width: 0;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}

.identity-card__purpose {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.identity-card__meta {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.identity-card__meta span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.identity-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  width: 100%;
  min-width: 0;
}

.identity-card__revocation {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.identity-card__actions {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  flex-shrink: 0;
}

.identity-card__actions .btn {
  min-height: 1.9rem;
  padding: 0.3rem 0.65rem;
  font-size: 0.72rem;
}

.identity-inspect {
  display: grid;
  gap: 0.75rem;
}

.identity-inspect__lede,
.identity-inspect__next {
  margin: 0;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  line-height: 1.5;
}

.identity-inspect__next {
  color: var(--color-text-secondary);
}

.identity-inspect__tech {
  min-width: 0;
  padding: 0.65rem 0.75rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: color-mix(in srgb, var(--color-bg-subtle) 55%, var(--color-bg-surface));
}

.identity-inspect__tech summary {
  cursor: pointer;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  font-weight: 600;
}

.identity-inspect__tech dl {
  display: grid;
  gap: 0.55rem;
  margin: 0.75rem 0 0;
}

.identity-inspect__tech dt {
  color: var(--color-text-tertiary);
  font-size: 0.7rem;
  font-weight: 650;
}

.identity-inspect__tech dd {
  margin: 0.15rem 0 0;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.identity-inspect__wrap {
  overflow-wrap: anywhere;
  word-break: break-all;
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
  margin: 0;
}

@media (max-width: 640px) {
  .identity-search {
    flex: 1 1 100%;
    max-width: none;
    width: 100%;
    height: auto;
    border: 0;
    background: transparent;
    justify-content: stretch;
    cursor: default;
  }

  .identity-search .search-icon-btn {
    display: none;
  }

  .identity-search .search-input {
    position: static;
    width: 100%;
    height: auto;
    opacity: 1;
    pointer-events: auto;
  }

  .identity-search .clear-btn {
    opacity: 1;
    pointer-events: auto;
  }

  .identity-catalog {
    grid-template-columns: 1fr;
  }

  .identity-card__footer {
    flex-wrap: wrap;
  }

  .identity-card__actions {
    width: 100%;
  }

  .identity-card__actions .btn {
    flex: 1 1 calc(50% - 0.2rem);
    min-height: 2.25rem;
    justify-content: center;
  }
}
</style>
