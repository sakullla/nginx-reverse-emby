<template>
  <div :class="['stat-card', colorClass]">
    <div class="stat-value">{{ value }}</div>
    <div class="stat-label">{{ label }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
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
  color: {
    type: String,
    default: 'blue',
    validator: (value) => ['blue', 'green', 'purple', 'gray'].includes(value)
  }
})

const colorClass = computed(() => `stat-${props.color}`)
</script>

<style scoped>
.stat-card {
  background: var(--color-bg-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--spacing-lg);
  display: flex;
  flex-direction: column;
  gap: var(--spacing-xs);
  transition: box-shadow var(--transition-fast);
}

.stat-card:hover {
  box-shadow: var(--shadow-sm);
}

.stat-value {
  font-size: var(--font-size-2xl);
  font-weight: var(--font-weight-bold);
  color: var(--color-heading);
  line-height: 1;
}

.stat-label {
  font-size: var(--font-size-sm);
  color: var(--color-text-muted);
}

/* Color Variants - Subtle */
.stat-blue .stat-value { color: var(--color-primary); }
.stat-green .stat-value { color: var(--color-success); }
.stat-purple .stat-value { color: #9333ea; }
.stat-gray .stat-value { color: var(--color-text-secondary); }
</style>
