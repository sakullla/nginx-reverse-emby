<template>
  <span :class="['badge', variant, size]">
    <slot />
  </span>
</template>

<script setup>
defineProps({
  variant: {
    type: String,
    default: 'primary',
    validator: (value) => ['primary', 'secondary', 'success', 'danger', 'warning', 'info'].includes(value)
  },
  size: {
    type: String,
    default: 'md',
    validator: (value) => ['sm', 'md', 'lg'].includes(value)
  }
})
</script>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-full);
  font-weight: var(--font-weight-semibold);
  line-height: 1;
  transition: all var(--transition-fast);
}

/* Sizes */
.badge.sm {
  padding: 0.25rem 0.5rem;
  font-size: var(--font-size-xs);
}

.badge.md {
  padding: var(--spacing-xs) var(--spacing-sm);
  font-size: var(--font-size-sm);
}

.badge.lg {
  padding: var(--spacing-sm) var(--spacing-md);
  font-size: var(--font-size-base);
}

/* Variants */
.badge.primary {
  background: var(--color-primary-bg);
  color: var(--color-primary-dark);
}

.badge.secondary {
  background: var(--color-secondary-bg);
  color: var(--color-secondary-dark);
}

.badge.success {
  background: var(--color-success-bg);
  color: var(--color-success-dark);
}

.badge.danger {
  background: var(--color-danger-bg);
  color: var(--color-danger-dark);
}

.badge.warning {
  background: var(--color-warning-bg);
  color: var(--color-warning-dark);
}

.badge.info {
  background: var(--color-info-bg);
  color: var(--color-info-dark);
}
</style>
