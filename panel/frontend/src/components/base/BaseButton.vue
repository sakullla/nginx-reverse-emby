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
  position: relative;
  overflow: hidden;
}

button.is-loading {
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
  border: 2.5px solid rgba(255, 255, 255, 0.3);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: button-spin 0.8s linear infinite;
}

button.secondary .spinner-mini {
  border-top-color: var(--color-primary);
  border-color: rgba(var(--color-primary-rgb, 37, 99, 235), 0.1);
}

@keyframes button-spin {
  to { transform: translate(-50%, -50%) rotate(360deg); }
}
</style>
