<template>
  <button
    :type="type"
    :disabled="disabled || loading"
    :class="[variant, { loading }]"
    @click="handleClick"
  >
    <span v-if="loading" class="loading-icon">⏳</span>
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
button.loading {
  position: relative;
}

.loading-icon {
  margin-right: 0.5rem;
}
</style>
