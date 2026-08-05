<template>
  <PkiSection
    title="告警与进行中操作"
    description="异常与进行中优先；同状态按最近更新时间倒序，每页最多 5 条。"
    eyebrow="优先处置"
    tone="attention"
    aria-label="告警与处置"
    collapsible
    storage-key="nre.pki.section.attention"
  >
    <template #actions>
      <BaseButton variant="secondary" size="sm" @click="$emit('rotate-ca')">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <polyline points="23 4 23 10 17 10"/>
          <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
        </svg>
        日常 CA 轮转
      </BaseButton>
      <BaseButton variant="danger-soft" size="sm" @click="$emit('emergency-ca')">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
          <line x1="12" y1="9" x2="12" y2="13"/>
          <line x1="12" y1="17" x2="12.01" y2="17"/>
        </svg>
        紧急 CA 轮转
      </BaseButton>
    </template>

    <div class="pki-attention">
      <div class="pki-attention__block">
        <div class="pki-attention__block-head">
          <h3>告警</h3>
          <BaseBadge :tone="alerts.length ? 'warning' : 'neutral'" size="sm">{{ alerts.length }}</BaseBadge>
        </div>

        <div v-if="alerts.length" class="pki-stack">
          <article
            v-for="alert in alerts"
            :key="alertField(alert, 'id')"
            data-test="alert-row"
            class="pki-alert"
            :class="`pki-alert--${String(alertField(alert, 'level')).toLowerCase()}`"
          >
            <div class="pki-alert__main">
              <div class="pki-alert__title">
                <PkiStatusBadge :status="alertField(alert, 'level')" :label="alertLevelLabel(alertField(alert, 'level'))" dot />
                <strong>{{ alertKindLabel(alertField(alert, 'kind')) }}</strong>
              </div>
              <p class="pki-alert__reason">{{ alertField(alert, 'reason') || '未提供原因' }}</p>
              <div class="pki-alert__meta">
                <BaseBadge tone="neutral" subtone="secondary" size="sm">{{ alertField(alert, 'object_type') || 'object' }}</BaseBadge>
                <span class="mono">{{ alertField(alert, 'object_id') || '—' }}</span>
              </div>
            </div>
            <time class="pki-alert__time">{{ formatDate(alertField(alert, 'last_seen')) }}</time>
          </article>
        </div>
        <div v-else class="pki-empty">
          <div class="pki-empty__icon" aria-hidden="true">✓</div>
          <p>当前没有内部 PKI 告警。</p>
        </div>

        <ListPagination
          v-if="alertTotal > pageSize"
          data-test="alert-pagination"
          :page="alertPage"
          :page-size="pageSize"
          :total="alertTotal"
          @update:page="$emit('update:alertPage', $event)"
        />
      </div>

      <div v-if="operations.length" class="pki-attention__block pki-attention__block--ops">
        <div class="pki-attention__block-head">
          <div>
            <h3>操作进度</h3>
            <p>提交后的轮转、撤销、备份等异步结果。</p>
          </div>
          <BaseBadge tone="primary" size="sm">{{ operationTotal }}</BaseBadge>
        </div>

        <div class="pki-stack">
          <article v-for="operation in operations" :key="operation.id" data-test="operation-row" class="pki-op">
            <div class="pki-op__main">
              <strong>{{ operationLabel(operation.kind) }}</strong>
              <span class="mono">{{ operation.target_id || operation.id }}</span>
            </div>
            <div class="pki-op__state">
              <PkiStatusBadge :status="operation.state" :label="operationStateLabel(operation.state)" />
              <span v-if="operation.phase" class="pki-op__phase">{{ operation.phase }}</span>
              <span v-if="operation.last_error" class="danger-text">{{ operation.last_error }}</span>
              <span v-if="operationErrors[operation.id]" class="danger-text">
                状态查询失败（{{ operationErrors[operation.id].status || 'network' }}）：{{ operationErrors[operation.id].message }}
              </span>
            </div>
            <div class="pki-op__actions">
              <button
                type="button"
                class="btn btn--secondary btn--sm"
                @click="$emit('refresh-operation', operation.id)"
              >查询状态</button>
              <button
                v-if="operation.terminal || operationErrors[operation.id]?.status === 404"
                type="button"
                class="btn btn--ghost btn--sm"
                @click="$emit('forget-operation', operation.id)"
              >本机移除</button>
            </div>
          </article>
        </div>

        <ListPagination
          v-if="operationTotal > pageSize"
          data-test="operation-pagination"
          :page="operationPage"
          :page-size="pageSize"
          :total="operationTotal"
          @update:page="$emit('update:operationPage', $event)"
        />
      </div>
    </div>
  </PkiSection>
</template>

<script setup>
import BaseBadge from '../base/BaseBadge.vue'
import BaseButton from '../base/BaseButton.vue'
import ListPagination from '../common/ListPagination.vue'
import PkiSection from './PkiSection.vue'
import PkiStatusBadge from './PkiStatusBadge.vue'

defineProps({
  alerts: { type: Array, default: () => [] },
  alertPage: { type: Number, default: 1 },
  alertTotal: { type: Number, default: 0 },
  operations: { type: Array, default: () => [] },
  operationPage: { type: Number, default: 1 },
  operationTotal: { type: Number, default: 0 },
  operationErrors: { type: Object, default: () => ({}) },
  pageSize: { type: Number, default: 5 },
  alertField: { type: Function, required: true },
  alertLevelLabel: { type: Function, required: true },
  alertKindLabel: { type: Function, required: true },
  operationLabel: { type: Function, required: true },
  operationStateLabel: { type: Function, required: true },
  formatDate: { type: Function, required: true },
})

defineEmits([
  'rotate-ca',
  'emergency-ca',
  'refresh-operation',
  'forget-operation',
  'update:alertPage',
  'update:operationPage',
])
</script>

<style scoped>
.pki-attention {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.pki-attention__block-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.pki-attention__block-head h3 {
  margin: 0 0 0.15rem;
  font-size: var(--text-base);
  color: var(--color-text-primary);
}

.pki-attention__block-head p {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
}

.pki-attention__block--ops {
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
}

.pki-stack {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.pki-alert,
.pki-op {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: color-mix(in srgb, var(--color-bg-subtle) 40%, var(--color-bg-surface));
}

.pki-alert {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
}

.pki-alert--critical,
.pki-alert--failed_closed {
  border-color: color-mix(in srgb, var(--color-danger) 40%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-danger) 5%, var(--color-bg-surface));
}

.pki-alert--warning {
  border-color: color-mix(in srgb, var(--color-warning) 40%, var(--color-border-default));
}

.pki-alert__main,
.pki-op__main,
.pki-op__state,
.pki-op__actions {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  min-width: 0;
}

.pki-alert__title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.pki-alert__reason {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  line-height: 1.45;
}

.pki-alert__meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
}

.pki-alert__time {
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  white-space: nowrap;
}

.pki-op {
  grid-template-columns: minmax(140px, 0.9fr) minmax(180px, 1.4fr) auto;
  align-items: center;
}

.pki-op__main strong {
  color: var(--color-text-primary);
}

.pki-op__main span,
.pki-op__phase {
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
}

.pki-op__actions {
  align-items: flex-end;
  flex-direction: row;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.35rem;
}

.pki-op__actions .btn {
  min-height: 1.85rem;
  padding: 0.28rem 0.6rem;
  font-size: 0.72rem;
}

.pki-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--color-text-tertiary);
  text-align: center;
}

.pki-empty__icon {
  width: 2rem;
  height: 2rem;
  border-radius: var(--radius-full);
  display: grid;
  place-items: center;
  background: color-mix(in srgb, var(--color-success) 12%, var(--color-bg-subtle));
  color: var(--color-success);
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
  font-size: var(--text-xs);
}

@media (max-width: 760px) {
  .pki-alert,
  .pki-op {
    grid-template-columns: 1fr;
  }

  .pki-op__actions {
    justify-content: flex-start;
  }

  .pki-op__actions .btn,
  .pki-section :deep(.pki-section__actions .btn) {
    min-height: 2.25rem;
  }
}
</style>
