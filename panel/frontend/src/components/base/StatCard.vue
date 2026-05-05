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
  background: var(--gradient-surface, var(--color-bg-surface));
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-5);
  box-shadow: var(--shadow-sm);
  transition: all var(--duration-normal) var(--ease-default);
  position: relative;
  overflow: hidden;
}

.stat-card:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
  border-color: var(--color-border-strong);
}

.stat-card__icon {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-lg);
  margin-bottom: var(--space-3);
}

.stat-card--primary .stat-card__icon {
  background: var(--gradient-primary-soft);
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

.stat-card__value {
  font-size: 1.75rem;
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 var(--space-1);
  letter-spacing: -0.02em;
  line-height: 1.2;
}

.stat-card__label {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
  margin: 0;
  font-weight: 500;
}

.stat-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  background: var(--gradient-primary);
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-default);
}

.stat-card--success::before { background: linear-gradient(90deg, var(--color-success), var(--color-success-glow)); }
.stat-card--warning::before { background: linear-gradient(90deg, var(--color-warning), var(--color-warning-glow)); }
.stat-card--danger::before { background: linear-gradient(90deg, var(--color-danger), var(--color-danger-glow)); }

.stat-card:hover::before {
  opacity: 1;
}
</style>
