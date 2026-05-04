<template>
  <div class="collapsible-section" :class="{ 'collapsible-section--collapsed': !expanded }">
    <button class="collapsible-section__header" type="button" @click="expanded = !expanded">
      <div class="collapsible-section__title-group">
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
  defaultExpanded: { type: Boolean, default: false }
})

const expanded = ref(props.defaultExpanded)
</script>

<style scoped>
.collapsible-section {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  overflow: hidden;
}
.collapsible-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 0.875rem 1rem;
  background: transparent;
  border: none;
  cursor: pointer;
  font-family: inherit;
  text-align: left;
}
.collapsible-section__header:hover {
  background: var(--color-bg-hover);
}
.collapsible-section__title-group {
  display: flex;
  align-items: center;
  gap: 0.5rem;
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
.collapsible-section--collapsed .collapsible-section__chevron {
  transform: rotate(-90deg);
}
.collapsible-section__body {
  padding: 0 1rem 1rem;
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
