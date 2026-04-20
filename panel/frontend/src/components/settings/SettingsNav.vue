<template>
  <nav class="settings-nav">
    <div class="settings-nav__label">设置</div>
    <button
      v-for="tab in tabs"
      :key="tab.id"
      class="settings-nav__item"
      :class="{ active: activeTab === tab.id }"
      @click="$emit('update:activeTab', tab.id)"
    >
      <span class="settings-nav__icon">{{ tab.icon }}</span>
      <span class="settings-nav__text">{{ tab.label }}</span>
    </button>
  </nav>
</template>

<script setup>
defineProps({
  activeTab: { type: String, required: true }
})

defineEmits(['update:activeTab'])

const tabs = [
  { id: 'general', icon: '⚙️', label: '通用' },
  { id: 'data', icon: '💾', label: '数据管理' },
  { id: 'about', icon: 'ℹ️', label: '关于' }
]
</script>

<style scoped>
.settings-nav {
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 1.5rem 0;
  min-width: 160px;
  flex-shrink: 0;
}
.settings-nav__label {
  padding: 0 1.25rem 1rem;
  font-size: 0.8rem;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-weight: 600;
}
.settings-nav__item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.6rem 1.25rem;
  border: none;
  background: none;
  cursor: pointer;
  font-size: 0.9rem;
  color: var(--color-text-secondary);
  border-left: 3px solid transparent;
  transition: all 0.15s var(--ease-default);
  width: 100%;
  text-align: left;
}
.settings-nav__item:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-subtle);
}
.settings-nav__item.active {
  color: var(--color-text-primary);
  font-weight: 600;
  border-left-color: var(--color-primary);
  background: var(--color-primary-subtle);
}
.settings-nav__icon { font-size: 1rem; }

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
    padding: 0.75rem 1.25rem;
    border-left: none;
    border-bottom: 2px solid transparent;
    white-space: nowrap;
  }
  .settings-nav__item.active {
    border-left-color: transparent;
    border-bottom-color: var(--color-primary);
  }
}
</style>
