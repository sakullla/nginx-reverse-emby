<template>
  <button
    :type="type"
    :disabled="disabled || loading"
    :class="[variant, { 'is-loading': loading }]"
    @click="handleClick"
  >
    <span v-if="loading" class="spinner-mini"></span>
    <slot />
  </button>
</template>

<script setup>
defineProps({
  type: {
    type: String,
    default: 'button',
    validator: (value) => ['button', 'submit', 'reset'].includes(value)
  },
  variant: {
    type: String,
    default: 'primary',
    validator: (value) => ['primary', 'secondary', 'danger', 'success'].includes(value)
  },
  disabled: {
    type: Boolean,
    default: false
  },
  loading: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['click'])

const handleClick = (event) => {
  emit('click', event)
}
</script>

<style scoped>
button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.375rem;
  padding: 10px 24px;
  border-radius: var(--radius-full);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: 1.5px solid transparent;
  font-family: inherit;
  text-decoration: none;
  white-space: nowrap;
  position: relative;
  overflow: hidden;
  line-height: 1.25;
  background: var(--color-primary);
  color: white;
}

button.secondary {
  background: transparent;
  color: var(--color-text-secondary);
  border: 1.5px solid var(--color-border-default);
}

button.secondary:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

button.danger {
  background: var(--color-danger);
}

button.danger:hover:not(:disabled) {
  background: #dc2626;
}

button.success {
  background: var(--color-success);
}

button.success:hover:not(:disabled) {
  background: #059669;
}

button:hover:not(:disabled) {
  background: var(--color-primary-hover);
  transform: translateY(-1px);
}

button:hover:not(:disabled):active {
  transform: translateY(0);
}

button.is-loading {
  color: transparent !important;
  pointer-events: none;
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

.spinner-mini {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 18px;
  height: 18px;
  border: 2.5px solid rgba(255, 255, 255, 0.3);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: button-spin 0.8s linear infinite;
}

button.secondary .spinner-mini {
  border-top-color: var(--color-primary);
  border-color: rgba(0, 0, 0, 0.08);
}

@keyframes button-spin {
  to { transform: translate(-50%, -50%) rotate(360deg); }
}
</style>
