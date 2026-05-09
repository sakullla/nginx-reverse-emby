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
      <div v-if="subLabel" class="stat-card__sub-label">{{ subLabel }}</div>
      <div class="stat-card__label">{{ label }}</div>
      <div v-if="progress != null" class="stat-card__progress">
        <div class="stat-card__progress-track">
          <div class="stat-card__progress-fill" :style="{ width: progress + '%' }" />
        </div>
      </div>
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
  subLabel: { type: String, default: '' },
  progress: { type: Number, default: null },
  to: { type: [String, Object], default: null },
})
</script>

<style scoped>
.stat-card {
  background: var(--color-bg-surface);
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

.stat-card__value {
  font-size: 1.75rem;
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 var(--space-1);
  letter-spacing: -0.02em;
  line-height: 1.2;
}

.stat-card__sub-label {
  font-size: 0.75rem;
  color: var(--color-text-secondary);
  margin: 0 0 0.125rem;
  font-weight: 500;
}

.stat-card__label {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
  margin: 0;
  font-weight: 500;
}

.stat-card__progress {
  margin-top: var(--space-2);
}
.stat-card__progress-track {
  height: 4px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
}
.stat-card__progress-fill {
  height: 100%;
  border-radius: var(--radius-full);
  background: var(--color-primary);
  transition: width 0.3s;
}
.stat-card--success .stat-card__progress-fill { background: var(--color-success); }
.stat-card--warning .stat-card__progress-fill { background: var(--color-warning); }
.stat-card--danger .stat-card__progress-fill { background: var(--color-danger); }

.stat-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  background: var(--color-primary);
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-default);
}

.stat-card--success::before { background: var(--color-success); }
.stat-card--warning::before { background: var(--color-warning); }
.stat-card--danger::before { background: var(--color-danger); }

.stat-card:hover::before {
  opacity: 1;
}
</style>
