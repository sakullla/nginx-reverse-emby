<template>
  <div :class="['stat-card', variant]">
    <div class="stat-content">
      <div class="stat-icon" v-if="icon">
        {{ icon }}
      </div>
      <div class="stat-details">
        <div class="stat-value">{{ value }}</div>
        <div class="stat-label">{{ label }}</div>
      </div>
    </div>
  </div>
</template>

<script setup>
defineProps({
  value: {
    type: [String, Number],
    required: true
  },
  label: {
    type: String,
    required: true
  },
  icon: {
    type: String,
    default: ''
  },
  variant: {
    type: String,
    default: 'primary',
    validator: (value) => ['primary', 'secondary', 'success', 'info'].includes(value)
  }
})
</script>

<style scoped>
/* Styles are in layout.css for better organization */
</style>
