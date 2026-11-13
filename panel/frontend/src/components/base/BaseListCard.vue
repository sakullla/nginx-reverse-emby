<template>
  <component
    :is="as"
    class="base-list-card"
    :class="{
      'base-list-card--clickable': clickable,
      'base-list-card--disabled': disabled,
    }"
    :data-status="status"
    :tabindex="clickable ? 0 : null"
    @click="onClick"
    @keydown.enter="onKey"
    @keydown.space.prevent="onKey"
  >
    <header v-if="$slots['header-left'] || $slots['header-right']" class="base-list-card__header">
      <div v-if="$slots['header-left']" class="base-list-card__header-left">
        <slot name="header-left" />
      </div>
      <div
        v-if="$slots['header-right']"
        class="base-list-card__header-right"
        @click.stop
        @keydown.enter.stop
        @keydown.space.stop
      >
        <slot name="header-right" />
      </div>
    </header>

    <div v-if="title" class="base-list-card__title" :title="title">
      <template v-for="(part, index) in titleParts" :key="index">{{ part }}<wbr v-if="index < titleParts.length - 1" /></template>
    </div>

    <div v-if="$slots.default" class="base-list-card__body">
      <slot />
    </div>

    <footer
      v-if="$slots.footer"
      class="base-list-card__footer"
      @click.stop
      @keydown.enter.stop
      @keydown.space.stop
    >
      <slot name="footer" />
    </footer>
  </component>
</template>

<script setup>
import { computed } from 'vue'
import { softBreakParts } from '../../utils/softBreak.js'

const props = defineProps({
  status: {
    type: String,
    default: null,
    validator: (v) => v === null || ['success', 'warning', 'danger', 'neutral'].includes(v),
  },
  disabled: { type: Boolean, default: false },
  clickable: { type: Boolean, default: true },
  title: { type: String, default: '' },
  as: { type: String, default: 'article' },
})

const emit = defineEmits(['click'])

const titleParts = computed(() => softBreakParts(props.title))

function onClick(e) {
  if (!props.clickable) return
  // Dragging to copy a hostname should not also open the editor.
  const selection = window.getSelection?.()
  if (selection && !selection.isCollapsed && e.currentTarget.contains(selection.anchorNode)) return
  emit('click', e)
}

function onKey(e) {
  if (!props.clickable) return
  emit('click', e)
}
</script>

<style scoped>
.base-list-card {
  position: relative;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  overflow: hidden;
  box-shadow: var(--shadow-xs);
  transition: border-color var(--duration-fast) var(--ease-default),
    box-shadow var(--duration-normal) var(--ease-default);
}

.base-list-card--clickable {
  cursor: pointer;
}

.base-list-card--clickable:hover {
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-md);
}

.base-list-card--clickable:focus-visible {
  outline: none;
  border-color: var(--color-border-focus);
  box-shadow: var(--shadow-focus);
}

.base-list-card--disabled {
  background: var(--color-bg-subtle);
  box-shadow: none;
}

.base-list-card--disabled .base-list-card__title {
  color: var(--color-text-tertiary);
}

.base-list-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2-5);
  min-width: 0;
  min-height: 26px;
}

.base-list-card__header-left {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
  flex-wrap: wrap;
  min-width: 0;
}

.base-list-card__header-right {
  display: flex;
  align-items: center;
  gap: 1px;
  flex-shrink: 0;
  transition: opacity var(--duration-fast) var(--ease-default);
}

/* Pointer devices: reveal secondary actions on intent to keep cards calm. */
@media (hover: hover) and (pointer: fine) {
  .base-list-card__header-right {
    opacity: 0.35;
  }

  .base-list-card:hover .base-list-card__header-right,
  .base-list-card:focus-within .base-list-card__header-right {
    opacity: 1;
  }
}

.base-list-card__title {
  font-family: var(--font-sans);
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.4;
  letter-spacing: -0.015em;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: break-word;
  word-break: normal;
}

.base-list-card__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.base-list-card__footer {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  min-width: 0;
  margin-top: auto;
  padding-top: var(--space-1);
}

@media (max-width: 640px) {
  .base-list-card__header {
    flex-wrap: wrap;
  }

  .base-list-card__header-right {
    margin-left: auto;
  }
}
</style>
