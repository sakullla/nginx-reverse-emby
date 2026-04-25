<template>
  <button
    type="button"
    class="base-icon-button"
    :class="[`base-icon-button--${tone}`, `base-icon-button--${size}`]"
    :title="title"
    :aria-label="title"
    :disabled="disabled"
    @click="onClick"
  >
    <slot />
  </button>
</template>

<script setup>
const props = defineProps({
  tone: {
    type: String,
    default: 'default',
    validator: (v) => ['default', 'danger', 'warning', 'success', 'primary'].includes(v),
  },
  size: {
    type: String,
    default: 'sm',
    validator: (v) => ['sm', 'md'].includes(v),
  },
  title: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
})

const emit = defineEmits(['click'])

function onClick(e) {
  e.stopPropagation()
  if (props.disabled) return
  emit('click', e)
}
</script>

<style scoped>
.base-icon-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  border-radius: var(--radius-md);
  transition: background 0.15s, color 0.15s, transform 0.15s;
  padding: 0;
  flex-shrink: 0;
}

.base-icon-button:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.base-icon-button:active:not(:disabled) {
  transform: scale(0.95);
}

/* Sizes */
.base-icon-button--sm {
  width: 28px;
  height: 28px;
}
.base-icon-button--md {
  width: 36px;
  height: 36px;
}

/* Hover tones */
.base-icon-button--default:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.base-icon-button--danger:hover:not(:disabled) {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
.base-icon-button--warning:hover:not(:disabled) {
  background: var(--color-warning-50);
  color: var(--color-warning);
}
.base-icon-button--success:hover:not(:disabled) {
  background: var(--color-success-50);
  color: var(--color-success);
}
.base-icon-button--primary:hover:not(:disabled) {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
</style>
