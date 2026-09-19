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

    <div v-if="title" class="base-list-card__title">{{ title }}</div>

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

function onClick(e) {
  if (!props.clickable) return
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
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  overflow: hidden;
  box-shadow: var(--shadow-xs);
  transition: border-color 150ms var(--ease-default, cubic-bezier(0.4, 0, 0.2, 1)),
    transform 150ms var(--ease-default, cubic-bezier(0.4, 0, 0.2, 1)),
    box-shadow 200ms var(--ease-default, cubic-bezier(0.4, 0, 0.2, 1));
}

.base-list-card--clickable {
  cursor: pointer;
}

.base-list-card--clickable:hover {
  border-color: var(--color-primary-300);
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
}

.base-list-card--clickable:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus, 0 0 0 3px var(--color-primary-subtle));
}

.base-list-card--clickable:active {
  transform: translateY(0);
}

.base-list-card--disabled {
  opacity: 0.58;
}

.base-list-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.6rem;
  min-width: 0;
  min-height: 26px;
}

.base-list-card__header-left {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  flex-wrap: wrap;
  min-width: 0;
}

.base-list-card__header-right {
  display: flex;
  align-items: center;
  gap: 0.15rem;
  flex-shrink: 0;
}

.base-list-card__title {
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-primary);
  line-height: 1.35;
  letter-spacing: -0.01em;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.base-list-card__body {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-width: 0;
}

.base-list-card__footer {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem;
  min-width: 0;
  margin-top: auto;
  padding-top: 0.05rem;
}
</style>
