<template>
  <BaseModal
    :model-value="Boolean(service)"
    title="删除服务"
    subtitle="确认将停止的入口与会保留的共享资源。"
    size="md"
    role="alertdialog"
    show-footer
    fit-content-on-mobile
    :close-on-click-modal="!submitting"
    data-test="service-delete-modal"
    @update:model-value="handleModalUpdate"
  >
    <div v-if="service" class="service-delete" data-testid="service-delete">
      <div class="service-delete__target">
        <svg
          class="service-delete__icon"
          width="18"
          height="18"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
        <div class="service-delete__target-info">
          <BaseBadge
            v-if="service.name || service.entry_url"
            class="service-delete__target-badge"
            tone="danger"
            size="sm"
          >
            {{ service.name || service.entry_url }}
          </BaseBadge>
          <code v-if="service.name && service.entry_url" class="service-delete__target-url">
            {{ service.entry_url }}
          </code>
        </div>
      </div>

      <div class="service-delete__columns">
        <div class="service-delete__panel">
          <p class="service-delete__panel-title">将停止的入口</p>
          <ul class="service-delete__list">
            <li
              v-for="(entry, index) in impact.stopped"
              :key="`${entry.rule_id}-${index}`"
              class="service-delete__item"
            >
              <code class="service-delete__mono">{{ entry.url || '未命名入口' }}</code>
              <span class="service-delete__sep"> · </span>
              <span>{{ agentLabel(entry.agent_id) }}</span>
            </li>
            <li v-if="!impact.stopped.length" class="service-delete__item service-delete__item--muted">
              当前没有可停止的入口规则
            </li>
          </ul>
        </div>

        <div class="service-delete__panel">
          <p class="service-delete__panel-title">会保留的共享资源</p>
          <ul class="service-delete__list">
            <li
              v-for="(item, index) in impact.retained"
              :key="`${item.kind}-${index}`"
              class="service-delete__item"
            >
              <BaseBadge tone="neutral" subtone="secondary" size="sm">
                {{ retainedResourceKindLabel(item.kind) }}
              </BaseBadge>
              <span v-if="item.id" class="service-delete__mono"> {{ item.id }}</span>
              <span>：{{ item.reason }}</span>
            </li>
          </ul>
        </div>
      </div>

      <p class="service-delete__warning">
        节点尚未应用删除时，服务会留在列表并显示待生效。这不表示入口已经关闭。
      </p>

      <div v-if="error" class="service-delete__error" role="alert" data-testid="service-delete-error">
        <svg
          class="service-delete__error-icon"
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="9" />
          <path d="M12 8v4" />
          <path d="M12 16h.01" />
        </svg>
        <span>{{ error }}</span>
      </div>
    </div>

    <template #footer>
      <div class="service-delete__actions">
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="submitting"
          @click="$emit('cancel')"
        >
          返回
        </button>
        <button
          type="button"
          class="btn btn--danger"
          :disabled="submitting"
          data-testid="service-delete-confirm"
          @click="$emit('confirm')"
        >
          {{ submitting ? '删除中...' : '确认删除' }}
        </button>
      </div>
    </template>
  </BaseModal>
</template>

<script setup>
import { computed } from 'vue'
import BaseModal from '../base/BaseModal.vue'
import BaseBadge from '../base/BaseBadge.vue'
import { deletionImpact, retainedResourceKindLabel } from '../../utils/accessService.js'

const props = defineProps({
  service: { type: Object, default: null },
  agents: { type: Array, default: () => [] },
  submitting: { type: Boolean, default: false },
  error: { type: String, default: '' },
})

const emit = defineEmits(['cancel', 'confirm'])

const impact = computed(() => deletionImpact(props.service || {}))

function agentLabel(id) {
  const agent = (props.agents || []).find((item) => String(item.id) === String(id))
  return agent?.name || id || '—'
}

function handleModalUpdate(nextOpen) {
  if (!nextOpen && !props.submitting) {
    emit('cancel')
  }
}
</script>

<style scoped>
.service-delete {
  display: flex;
  flex-direction: column;
  gap: var(--space-3-5, 0.875rem);
  min-width: 0;
}

.service-delete__target {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2-5);
  padding: var(--space-3) var(--space-3-5, 0.875rem);
  border-radius: var(--radius-lg);
  border: 1px solid color-mix(in srgb, var(--color-danger) 24%, var(--color-border-default));
  background: var(--color-danger-50);
  min-width: 0;
}

.service-delete__icon {
  color: var(--color-danger);
  flex-shrink: 0;
  margin-top: 2px;
}

.service-delete__target-info {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
  min-width: 0;
}

.service-delete__target-badge {
  max-width: 100%;
  min-width: 0;
  height: auto;
  min-height: 1.25rem;
  padding-block: 0.15rem;
  white-space: normal;
  line-height: 1.35;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-delete__target-url {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-delete__columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}

.service-delete__panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3-5, 0.875rem);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  min-width: 0;
}

.service-delete__panel-title {
  margin: 0;
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
}

.service-delete__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
}

.service-delete__item {
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.service-delete__item--muted {
  color: var(--color-text-tertiary);
}

.service-delete__mono {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-delete__sep {
  color: var(--color-text-muted);
}

.service-delete__warning {
  margin: 0;
  padding: var(--space-3) var(--space-3-5, 0.875rem);
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-warning) 28%, transparent);
  background: var(--color-warning-50);
  color: var(--color-text-primary);
  font-size: var(--text-xs);
  line-height: 1.55;
}

.service-delete__error {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-danger) 28%, transparent);
  background: var(--color-danger-50);
  color: var(--color-danger);
  font-size: var(--text-sm);
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.service-delete__error-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.service-delete__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
  width: 100%;
}

@media (max-width: 640px) {
  .service-delete__columns {
    grid-template-columns: 1fr;
  }

  .service-delete__actions {
    justify-content: stretch;
  }

  .service-delete__actions > .btn {
    flex: 1 1 auto;
  }
}
</style>
