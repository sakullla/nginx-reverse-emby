<template>
  <button
    :type="type"
    :disabled="disabled || loading"
    class="btn"
    :class="[
      variantClass,
      sizeClass,
      { 'is-loading': loading }
    ]"
    @click="handleClick"
  >
    <span v-if="loading" class="spinner-mini" aria-hidden="true"></span>
    <slot />
  </button>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  type: {
    type: String,
    default: 'button',
    validator: (value) => ['button', 'submit', 'reset'].includes(value)
  },
  variant: {
    type: String,
    default: 'primary',
    validator: (value) => ['primary', 'secondary', 'danger', 'danger-soft', 'ghost', 'success'].includes(value)
  },
  size: {
    type: String,
    default: 'md',
    validator: (value) => ['sm', 'md', 'lg'].includes(value)
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

const variantClass = computed(() => {
  switch (props.variant) {
    case 'secondary':
      return 'btn--secondary'
    case 'danger':
      return 'btn--danger'
    case 'danger-soft':
      return 'btn--danger-soft'
    case 'ghost':
      return 'btn--ghost'
    case 'success':
      return 'btn--primary base-button--success'
    default:
      return 'btn--primary'
  }
})

const sizeClass = computed(() => {
  if (props.size === 'sm') return 'btn--sm'
  if (props.size === 'lg') return 'btn--lg'
  return null
})

const handleClick = (event) => {
  emit('click', event)
}
</script>

<style scoped>
.btn.is-loading {
  color: transparent !important;
  pointer-events: none;
}

.spinner-mini {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 18px;
  height: 18px;
  border: 2.5px solid color-mix(in srgb, currentColor 30%, transparent);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: button-spin 0.8s linear infinite;
}

.btn--secondary .spinner-mini,
.btn--ghost .spinner-mini,
.btn--danger-soft .spinner-mini {
  border-color: color-mix(in srgb, var(--color-text-primary) 12%, transparent);
  border-top-color: var(--color-primary);
}

.base-button--success {
  background: var(--color-success) !important;
  border-color: var(--color-success) !important;
  box-shadow: none !important;
}

.base-button--success:hover:not(:disabled) {
  filter: brightness(0.95);
  background: var(--color-success) !important;
  border-color: var(--color-success) !important;
}

@keyframes button-spin {
  to { transform: translate(-50%, -50%) rotate(360deg); }
}
</style>
