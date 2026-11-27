<template>
  <div
    class="bulk-action-bar"
    :class="{ 'bulk-action-bar--busy': busy }"
    role="toolbar"
    aria-label="批量操作"
    @keydown.esc="!busy && $emit('cancel')"
  >
    <div class="bulk-action-bar__summary" aria-live="polite">
      <span class="bulk-action-bar__count">已选 {{ count }} 项</span>
      <span v-if="busy && progressText" class="bulk-action-bar__progress">{{ progressText }}</span>
    </div>
    <div class="bulk-action-bar__actions">
      <BaseButton
        v-if="showEnable"
        variant="secondary"
        size="sm"
        :disabled="actionsDisabled"
        data-bulk-action="enable"
        @click="$emit('enable')"
      >启用</BaseButton>
      <BaseButton
        v-if="showDisable"
        variant="secondary"
        size="sm"
        :disabled="actionsDisabled"
        data-bulk-action="disable"
        @click="$emit('disable')"
      >禁用</BaseButton>
      <BaseButton
        v-if="showDelete"
        variant="danger-soft"
        size="sm"
        :disabled="actionsDisabled"
        data-bulk-action="delete"
        @click="$emit('delete')"
      >删除</BaseButton>
      <BaseButton
        variant="ghost"
        size="sm"
        :disabled="busy"
        data-bulk-action="cancel"
        @click="$emit('cancel')"
      >取消</BaseButton>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import BaseButton from '../base/BaseButton.vue'

const props = defineProps({
  count: { type: Number, default: 0 },
  busy: { type: Boolean, default: false },
  /** Optional progress while busy: { done, total } */
  progress: { type: Object, default: null },
  showEnable: { type: Boolean, default: true },
  showDisable: { type: Boolean, default: true },
  showDelete: { type: Boolean, default: true }
})

defineEmits(['enable', 'disable', 'delete', 'cancel'])

const actionsDisabled = computed(() => props.busy || props.count === 0)

const progressText = computed(() => {
  const total = props.progress?.total ?? 0
  if (!total) return '处理中…'
  return `处理中 ${props.progress?.done ?? 0}/${total}`
})
</script>

<style scoped>
.bulk-action-bar {
  position: sticky;
  bottom: var(--space-4);
  z-index: var(--z-sticky, 20);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2) var(--space-3);
  margin: var(--space-3) 0;
  padding: var(--space-2) var(--space-2) var(--space-2) var(--space-4);
  border-radius: var(--radius-xl);
  border: 1px solid color-mix(in srgb, var(--color-primary) 30%, var(--color-border-default));
  background: var(--color-bg-surface-raised);
  box-shadow: var(--shadow-lg);
}

.bulk-action-bar__summary {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
}

.bulk-action-bar__count {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-primary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.bulk-action-bar__progress {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}

.bulk-action-bar__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

@media (max-width: 1023px) {
  /* Stay above the BottomNav. */
  .bulk-action-bar {
    bottom: calc(64px + env(safe-area-inset-bottom, 0px) + var(--space-2));
  }
}

@media (max-width: 640px) {
  .bulk-action-bar {
    padding: var(--space-2) var(--space-3);
  }

  .bulk-action-bar__actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
