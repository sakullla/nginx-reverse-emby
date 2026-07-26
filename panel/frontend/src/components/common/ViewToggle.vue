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
  display: inline-flex;
  align-items: center;
  gap: 2px;
  min-height: 34px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-full);
  padding: 3px;
  border: 1px solid var(--color-border-subtle);
  box-sizing: border-box;
}
.view-toggle__btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-full);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}
.view-toggle__btn:hover {
  color: var(--color-text-primary);
  background: color-mix(in srgb, var(--color-bg-hover) 70%, transparent);
}
.view-toggle__btn:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
}
.view-toggle__btn.active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}
</style>
