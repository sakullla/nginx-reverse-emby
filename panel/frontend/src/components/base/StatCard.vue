<template>
  <div :class="['stat-card', variant]">
    <div class="stat-content">
      <div class="stat-icon" v-if="icon" v-html="icon"></div>
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
.stat-icon :deep(svg) {
  width: 24px;
  height: 24px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

.stat-card.primary .stat-icon { color: var(--color-primary); }
.stat-card.secondary .stat-icon { color: var(--color-secondary); }
.stat-card.success .stat-icon { color: var(--color-success); }
.stat-card.info .stat-icon { color: var(--color-info); }
</style>
