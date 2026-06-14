<template>
  <nav class="settings-nav">
    <div class="settings-nav__label">{{ label }}</div>
    <button
      v-for="tab in tabs"
      :key="tab.id"
      class="settings-nav__item"
      :class="{ active: activeTab === tab.id }"
      @click="$emit('update:activeTab', tab.id)"
    >
      <span v-if="tab.icon" class="settings-nav__icon">{{ tab.icon }}</span>
      <span class="settings-nav__text">{{ tab.label }}</span>
    </button>
  </nav>
</template>

<script setup>
defineProps({
  activeTab: { type: String, required: true },
  tabs: { type: Array, required: true },
  label: { type: String, default: '设置' }
})

defineEmits(['update:activeTab'])
</script>

<style scoped>
.settings-nav {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-6) 0;
  min-width: 180px;
  flex-shrink: 0;
}
.settings-nav__label {
  padding: 0 var(--space-5) var(--space-3);
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-weight: var(--font-semibold);
}
.settings-nav__item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-4);
  margin: 0 var(--space-2);
  border: none;
  border-left: 3px solid transparent;
  background: none;
  cursor: pointer;
  font-family: inherit;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  border-radius: var(--radius-sm);
  transition: color var(--duration-fast) var(--ease-default),
              background-color var(--duration-fast) var(--ease-default),
              border-color var(--duration-fast) var(--ease-default);
  width: calc(100% - var(--space-4));
  text-align: left;
}
.settings-nav__item:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-subtle);
}
.settings-nav__item.active {
  color: var(--color-primary);
  font-weight: var(--font-semibold);
  border-left-color: var(--color-primary);
  background: var(--color-primary-subtle);
}
.settings-nav__icon { font-size: var(--text-base); line-height: 1; }

@media (max-width: 767px) {
  .settings-nav {
    flex-direction: row;
    padding: 0;
    min-width: unset;
    border-bottom: 1px solid var(--color-border-default);
    overflow-x: auto;
    gap: 0;
  }
  .settings-nav__label { display: none; }
  .settings-nav__item {
    padding: var(--space-3) var(--space-5);
    margin: 0;
    width: auto;
    border-left: none;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    white-space: nowrap;
  }
  .settings-nav__item.active {
    border-left-color: transparent;
    border-bottom-color: var(--color-primary);
    background: none;
  }
}
</style>
