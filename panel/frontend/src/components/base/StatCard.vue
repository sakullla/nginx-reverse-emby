<template>
  <component
    :is="to ? 'RouterLink' : 'div'"
    :to="to || undefined"
    class="stat-card"
    :class="[`stat-card--${tone}`, { 'stat-card--linked': !!to }]"
  >
    <div class="stat-card__icon">
      <slot name="icon" />
    </div>
    <div class="stat-card__data">
      <div class="stat-card__value">{{ value }}</div>
      <div class="stat-card__label">{{ label }}</div>
    </div>
  </component>
</template>

<script setup>
defineProps({
  tone: {
    type: String,
    default: 'primary',
    validator: (v) => ['primary', 'success', 'warning', 'danger'].includes(v),
  },
  value: { type: [String, Number], required: true },
  label: { type: String, required: true },
  to: { type: [String, Object], default: null },
})
</script>

<style scoped>
.stat-card {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 1.25rem;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  text-decoration: none;
  color: inherit;
}

.stat-card--linked {
  cursor: pointer;
  transition: border-color 150ms var(--ease-default, cubic-bezier(0.4, 0, 0.2, 1)),
    box-shadow 200ms var(--ease-default, cubic-bezier(0.4, 0, 0.2, 1));
}

.stat-card--linked:hover {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}

.stat-card__icon {
  width: 48px;
  height: 48px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.stat-card--primary .stat-card__icon {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.stat-card--success .stat-card__icon {
  background: var(--color-success-50);
  color: var(--color-success);
}

.stat-card--warning .stat-card__icon {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.stat-card--danger .stat-card__icon {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.stat-card__data {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.stat-card__value {
  font-size: 1.75rem;
  font-weight: 700;
  color: var(--color-text-primary);
  line-height: 1;
}

.stat-card__label {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
}
</style>
