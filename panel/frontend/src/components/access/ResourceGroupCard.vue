<script setup>
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import BaseListCard from '../base/BaseListCard.vue'
import { resourceGroupDisplayDescription, resourceGroupDisplayName } from '../../context/useAccessControl'

const props = defineProps({
  group: { type: Object, required: true },
  grantCount: { type: Number, default: 0 },
  resourceCount: { type: Number, default: 0 },
  canDelete: { type: Boolean, default: false },
  busy: { type: Boolean, default: false }
})

defineEmits(['manage', 'delete'])

function displayName() {
  return resourceGroupDisplayName(props.group)
}

function displayDescription() {
  return resourceGroupDisplayDescription(props.group)
}
</script>

<template>
  <BaseListCard
    class="rg-card"
    :data-test="`group-row-${group.id}`"
    clickable
    @click="$emit('manage', group)"
  >
    <template #header-left>
      <span class="rg-card__name" :title="displayName()">{{ displayName() }}</span>
      <BaseBadge :tone="group.builtin ? 'neutral' : 'primary'">
        {{ group.builtin ? '内置组' : '自定义组' }}
      </BaseBadge>
    </template>
    <template #header-right>
      <BaseIconButton title="管理" data-test="manage-group" @click="$emit('manage', group)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3" />
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
        </svg>
      </BaseIconButton>
      <BaseIconButton
        v-if="canDelete"
        tone="danger"
        title="删除"
        data-test="delete-group"
        :disabled="busy"
        @click="$emit('delete', group)"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6" />
          <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
        </svg>
      </BaseIconButton>
    </template>

    <p class="rg-card__desc" :title="displayDescription() || ''">{{ displayDescription() || '暂无说明' }}</p>
    <template #footer>
      <span class="rg-card__stat">授权 <strong data-test="group-grant-count">{{ grantCount }}</strong></span>
      <span class="rg-card__stat">资源 <strong data-test="group-resource-count">{{ resourceCount }}</strong></span>
    </template>
  </BaseListCard>
</template>

<style scoped>
.rg-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.rg-card :deep(.base-list-card__footer) {
  gap: 0.85rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}

.rg-card__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.rg-card__desc {
  margin: 0;
  min-height: 2.3em;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  line-height: 1.45;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
}

.rg-card__stat {
  color: var(--color-text-secondary);
  font-size: 0.75rem;
}

.rg-card__stat strong {
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}
</style>
