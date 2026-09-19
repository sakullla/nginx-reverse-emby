<script setup>
import BaseBadge from '../base/BaseBadge.vue'
import BaseIconButton from '../base/BaseIconButton.vue'
import BaseListCard from '../base/BaseListCard.vue'

const props = defineProps({
  user: { type: Object, required: true },
  roleNames: { type: Array, default: () => [] },
  busy: { type: Boolean, default: false }
})

defineEmits(['edit', 'enable', 'disable', 'reset', 'delete'])

function label() {
  return props.user.display_name || props.user.username || props.user.id || ''
}
</script>

<template>
  <BaseListCard
    class="user-card"
    :class="{ 'user-card--disabled': user.disabled }"
    :data-test="`user-row-${user.id}`"
    clickable
    @click="$emit('edit', user)"
  >
    <template #header-left>
      <span class="user-card__name" :title="label()">{{ label() }}</span>
      <BaseBadge :tone="user.disabled ? 'danger' : 'success'" dot>
        {{ user.disabled ? '已停用' : '已启用' }}
      </BaseBadge>
    </template>
    <template #header-right>
      <BaseIconButton title="编辑" data-test="edit-user" @click="$emit('edit', user)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
        </svg>
      </BaseIconButton>
      <BaseIconButton
        v-if="user.disabled"
        title="启用"
        data-test="enable-user"
        :disabled="busy"
        @click="$emit('enable', user)"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M5 12h14" />
          <path d="M12 5v14" />
        </svg>
      </BaseIconButton>
      <BaseIconButton
        v-else
        title="停用"
        data-test="disable-user"
        :disabled="busy"
        @click="$emit('disable', user)"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="6" y="6" width="12" height="12" rx="2" />
        </svg>
      </BaseIconButton>
      <BaseIconButton title="重置密码" data-test="reset-user" :disabled="busy" @click="$emit('reset', user)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="11" width="18" height="11" rx="2" />
          <path d="M7 11V7a5 5 0 0 1 10 0v4" />
        </svg>
      </BaseIconButton>
      <BaseIconButton title="删除" tone="danger" data-test="delete-user" :disabled="busy" @click="$emit('delete', user)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6" />
          <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
        </svg>
      </BaseIconButton>
    </template>

    <p class="user-card__username" data-test="user-username" :title="user.username">{{ user.username }}</p>
    <template #footer>
      <BaseBadge v-for="name in roleNames" :key="name" tone="neutral">{{ name }}</BaseBadge>
      <span v-if="!roleNames.length" class="user-card__empty">未分配角色</span>
    </template>
  </BaseListCard>
</template>

<style scoped>
.user-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.user-card :deep(.base-list-card__footer) {
  gap: 0.35rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}

.user-card__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.user-card--disabled .user-card__name {
  color: var(--color-text-secondary);
}

.user-card__username {
  margin: 0;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  font-family: var(--font-mono);
}

.user-card__empty {
  color: var(--color-text-muted);
  font-size: 0.75rem;
}
</style>
