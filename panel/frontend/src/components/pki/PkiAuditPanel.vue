<template>
  <div class="audit-panel">
    <div v-if="!hideHeader" class="audit-panel__head">
      <div>
        <div class="audit-panel__eyebrow">可追溯</div>
        <h2>安全审计</h2>
        <p>查询签发、续签、轮转、撤销、拒绝、备份与恢复事件；最新优先，每页最多 5 条。</p>
      </div>
    </div>

    <form class="audit-toolbar" @submit.prevent="$emit('search')">
      <div class="audit-toolbar__grid">
        <label class="audit-field">
          <span class="audit-field__label">操作</span>
          <input
            :value="filters.type"
            class="audit-field__input"
            placeholder="换证 / 撤销"
            @input="update('type', $event.target.value)"
          >
        </label>
        <label class="audit-field">
          <span class="audit-field__label">节点或身份</span>
          <input
            :value="filters.identity_id"
            class="audit-field__input"
            placeholder="节点名称或身份"
            @input="update('identity_id', $event.target.value)"
          >
        </label>
        <label class="audit-field">
          <span class="audit-field__label">来源</span>
          <input
            :value="filters.source"
            class="audit-field__input"
            placeholder="管理端 / 节点"
            @input="update('source', $event.target.value)"
          >
        </label>
        <label class="audit-field">
          <span class="audit-field__label">结果</span>
          <input
            :value="filters.result"
            class="audit-field__input"
            placeholder="成功 / 失败"
            @input="update('result', $event.target.value)"
          >
        </label>
        <div class="audit-toolbar__actions">
          <BaseButton type="submit" variant="secondary">查询</BaseButton>
          <BaseButton type="button" variant="ghost" @click="clearFilters">清空</BaseButton>
        </div>
      </div>
    </form>

    <div class="audit-list">
      <article v-for="event in events" :key="event.id" data-test="event-row" class="audit-row">
        <div class="audit-row__main">
          <div class="audit-row__title">
            <strong>{{ eventTypeLabel(event.type) }}</strong>
            <PkiStatusBadge
              :status="event.result === 'failed' || event.result === 'rejected' ? 'failed' : 'succeeded'"
              :label="eventResultLabel(event.result)"
            />
          </div>
          <span class="audit-row__object" :title="event.object_label || ''">
            {{ event.object_label || event.object_type_label || '—' }}
          </span>
          <span v-if="reasonLabel(event.reason)" class="audit-row__reason">{{ reasonLabel(event.reason) }}</span>
        </div>
        <div class="audit-row__meta">
          <span>{{ sourceLabel(event.source) }}<template v-if="event.operator_id"> · {{ event.operator_id }}</template></span>
          <time>{{ formatDate(event.occurred_at) }}</time>
        </div>
      </article>

      <div v-if="!events.length" class="pki-empty">
        <div class="pki-empty__icon" aria-hidden="true">∅</div>
        <p>当前筛选没有审计事件。</p>
      </div>
    </div>

    <ListPagination
      v-if="total > pageSize"
      data-test="event-pagination"
      :page="page"
      :page-size="pageSize"
      :total="total"
      @update:page="$emit('update:page', $event)"
    />
  </div>
</template>

<script setup>
import BaseButton from '../base/BaseButton.vue'
import ListPagination from '../common/ListPagination.vue'
import PkiStatusBadge from './PkiStatusBadge.vue'

const props = defineProps({
  events: { type: Array, default: () => [] },
  filters: { type: Object, required: true },
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 5 },
  total: { type: Number, default: 0 },
  formatDate: { type: Function, required: true },
  eventTypeLabel: { type: Function, default: (type) => type || '—' },
  eventResultLabel: { type: Function, default: (result) => result || '—' },
  sourceLabel: { type: Function, default: (source) => source || '—' },
  reasonLabel: { type: Function, default: (reason) => reason || '' },
  hideHeader: { type: Boolean, default: false },
})

const emit = defineEmits(['search', 'update:page', 'update:filters'])

function update(key, value) {
  emit('update:filters', { ...props.filters, [key]: value })
}

function clearFilters() {
  emit('update:filters', { type: '', identity_id: '', source: '', result: '' })
  emit('search')
}
</script>

<style scoped>
.audit-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}

.audit-panel__eyebrow {
  color: var(--color-primary);
  font-size: 0.7rem;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  margin-bottom: 0.2rem;
}

.audit-panel__head h2 {
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  font-size: var(--text-lg);
}

.audit-panel__head p {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
  line-height: 1.5;
}

.audit-toolbar {
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: color-mix(in srgb, var(--color-bg-subtle) 45%, var(--color-bg-surface));
}

.audit-toolbar__grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(10.5rem, 1fr));
  gap: var(--space-3);
  align-items: end;
}

.audit-field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-width: 0;
}

.audit-field__label {
  color: var(--color-text-tertiary);
  font-size: 0.7rem;
  font-weight: 700;
  letter-spacing: 0.02em;
}

.audit-field__input {
  width: 100%;
  box-sizing: border-box;
  min-height: 2.5rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  padding: 0.5rem 0.75rem;
  font: inherit;
  font-size: var(--text-sm);
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.audit-field__input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.audit-field__input::placeholder {
  color: var(--color-text-muted);
}

.audit-toolbar__actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
  padding-bottom: 1px;
}

.audit-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.audit-row {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-width: 0;
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.audit-row__main,
.audit-row__meta {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 0;
}

.audit-row__title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  min-width: 0;
}

.audit-row__title strong {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.audit-row__object,
.audit-row__meta span,
.audit-row__meta time {
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
}

.audit-row__object {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.audit-row__reason {
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
}

.audit-row__meta {
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.35rem 0.75rem;
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
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-xl);
  background: color-mix(in srgb, var(--color-bg-subtle) 55%, var(--color-bg-surface));
}

.pki-empty__icon {
  width: 2rem;
  height: 2rem;
  border-radius: var(--radius-md);
  display: grid;
  place-items: center;
  background: var(--color-bg-subtle);
  font-weight: 700;
}

.pki-empty p {
  margin: 0;
  font-size: var(--text-sm);
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

@media (min-width: 1920px) {
  .audit-toolbar {
    padding: var(--space-4);
  }
}

@media (max-width: 680px) {
  .audit-toolbar {
    padding: var(--space-3);
  }

  .audit-toolbar__grid {
    grid-template-columns: 1fr;
  }

  .audit-toolbar__actions {
    width: 100%;
  }

  .audit-toolbar__actions :deep(.btn),
  .audit-toolbar__actions .btn {
    flex: 1 1 auto;
    min-height: 2.35rem;
    justify-content: center;
  }

  .audit-row {
    padding: var(--space-3);
  }
}
</style>
