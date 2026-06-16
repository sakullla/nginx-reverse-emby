<template>
  <div class="view-toggle">
    <button
      class="view-toggle__btn"
      :class="{ active: view === 'card' }"
      title="卡片视图"
      @click="$emit('update:view', 'card')"
    >
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <rect x="3" y="3" width="7" height="7" rx="1"/>
        <rect x="14" y="3" width="7" height="7" rx="1"/>
        <rect x="3" y="14" width="7" height="7" rx="1"/>
        <rect x="14" y="14" width="7" height="7" rx="1"/>
      </svg>
    </button>
    <button
      class="view-toggle__btn"
      :class="{ active: view === 'list' }"
      title="列表视图"
      @click="$emit('update:view', 'list')"
    >
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <line x1="8" y1="6" x2="21" y2="6"/>
        <line x1="8" y1="12" x2="21" y2="12"/>
        <line x1="8" y1="18" x2="21" y2="18"/>
        <line x1="3" y1="6" x2="3.01" y2="6"/>
        <line x1="3" y1="12" x2="3.01" y2="12"/>
        <line x1="3" y1="18" x2="3.01" y2="18"/>
      </svg>
    </button>
  </div>
</template>

<script setup>
defineProps({
  view: { type: String, required: true }
})
defineEmits(['update:view'])
</script>

<style scoped>
.view-toggle {
  display: flex;
  gap: 2px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  padding: 2px;
  border: 1.5px solid var(--color-border-default);
}
.view-toggle__btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all 0.15s;
}
.view-toggle__btn.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}
</style>
