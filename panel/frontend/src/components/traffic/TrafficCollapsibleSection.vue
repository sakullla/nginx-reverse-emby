<template>
  <div class="collapsible-section" :class="{ 'collapsible-section--collapsed': !expanded }">
    <button
      class="collapsible-section__header"
      :class="{ 'collapsible-section__header--expanded': expanded }"
      type="button"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <div class="collapsible-section__title-group">
        <span v-if="icon" :class="[icon, 'collapsible-section__icon']" aria-hidden="true" />
        <h3 class="collapsible-section__title">{{ title }}</h3>
        <span v-if="subtitle" class="collapsible-section__subtitle">{{ subtitle }}</span>
      </div>
      <svg
        class="collapsible-section__chevron"
        width="16" height="16" viewBox="0 0 24 24"
        fill="none" stroke="currentColor" stroke-width="2"
      >
        <polyline points="6 9 12 15 18 9"/>
      </svg>
    </button>
    <Transition name="collapse">
      <div v-if="expanded" class="collapsible-section__body">
        <slot />
      </div>
    </Transition>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const props = defineProps({
  title: { type: String, required: true },
  subtitle: { type: String, default: '' },
  icon: { type: String, default: '' },
  defaultExpanded: { type: Boolean, default: false }
})

const expanded = ref(props.defaultExpanded)
</script>

<style scoped>
.collapsible-section {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  box-shadow: var(--shadow-xs);
}
.collapsible-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 0.75rem 1rem;
  background: transparent;
  border: none;
  border-bottom: 1px solid transparent;
  cursor: pointer;
  font-family: inherit;
  text-align: left;
}
.collapsible-section__header:hover {
  background: var(--color-bg-hover);
}
/* 展开态:header 加分隔线与浅底,与内容区形成层次 */
.collapsible-section__header--expanded {
  background: var(--color-bg-subtle);
  border-bottom-color: var(--color-border-subtle);
}
.collapsible-section__title-group {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.collapsible-section__icon {
  width: 1rem;
  height: 1rem;
  color: var(--color-text-muted);
  flex-shrink: 0;
}
.collapsible-section__title {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 600;
  color: var(--color-text-primary);
}
.collapsible-section__subtitle {
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
}
.collapsible-section__chevron {
  color: var(--color-text-tertiary);
  transition: transform var(--duration-normal) var(--ease-default);
  flex-shrink: 0;
}
/* 展开时 chevron 上指;收起时保持下指(不用 ">",避免误读为跳转) */
.collapsible-section__header--expanded .collapsible-section__chevron {
  transform: rotate(180deg);
}
.collapsible-section__body {
  padding: 0.75rem 1rem 1rem;
}
.collapse-enter-active,
.collapse-leave-active {
  transition: all var(--duration-normal) var(--ease-default);
  overflow: hidden;
}
.collapse-enter-from,
.collapse-leave-to {
  opacity: 0;
  max-height: 0;
  padding-top: 0;
  padding-bottom: 0;
}
</style>
