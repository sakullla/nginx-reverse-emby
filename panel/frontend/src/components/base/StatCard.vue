<template>
  <component
    :is="to ? 'RouterLink' : 'div'"
    :to="to || undefined"
    class="stat-card"
    :class="[`stat-card--${tone}`, `stat-card--${size}`, { 'stat-card--linked': !!to }]"
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
  size: {
    type: String,
    default: 'md',
    validator: (v) => ['md', 'lg'].includes(v),
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
  transition: box-shadow var(--duration-normal) var(--ease-default),
    border-color var(--duration-normal) var(--ease-default),
    transform var(--duration-normal) var(--ease-default);
  position: relative;
  overflow: hidden;
}

.stat-card:hover {
  box-shadow: var(--shadow-md);
  border-color: var(--color-border-strong);
}

.stat-card--linked:hover {
  transform: translateY(-1px);
}

.stat-card__icon {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-lg);
  margin-bottom: var(--space-3);
  transition: transform var(--duration-normal) var(--ease-default);
}

.stat-card:hover .stat-card__icon {
  transform: scale(1.05);
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
  display: inline-block;
  font-variant-numeric: tabular-nums;
  transition: transform var(--duration-normal) var(--ease-default);
}

.stat-card:hover .stat-card__value {
  transform: scale(1.03);
}

.stat-card__sub-label {
  font-size: 0.75rem;
  color: var(--color-text-secondary);
  margin: 0 0 0.125rem;
  font-weight: 500;
  opacity: 0.85;
}

.stat-card__label {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
  margin: 0;
  font-weight: 500;
}

.stat-card__progress {
  margin-top: var(--space-3);
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
  transition: width 0.5s var(--ease-default);
}
.stat-card--success .stat-card__progress-fill { background: var(--color-success); }
.stat-card--warning .stat-card__progress-fill { background: var(--color-warning); }
.stat-card--danger .stat-card__progress-fill { background: var(--color-danger); }

.stat-card::after {
  content: '';
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 3px;
  background: var(--color-primary);
  transform: scaleY(0);
  transform-origin: bottom;
  transition: transform var(--duration-normal) var(--ease-default);
}

.stat-card--success::after { background: var(--color-success); }
.stat-card--warning::after { background: var(--color-warning); }
.stat-card--danger::after { background: var(--color-danger); }

.stat-card:hover::after {
  transform: scaleY(1);
}

.stat-card--linked {
  text-decoration: none;
}
.stat-card--linked::before {
  content: '';
  position: absolute;
  right: var(--space-4);
  top: 50%;
  transform: translateY(-50%);
  width: 6px;
  height: 6px;
  border-top: 1.5px solid var(--color-text-tertiary);
  border-right: 1.5px solid var(--color-text-tertiary);
  rotate: 45deg;
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-default), transform var(--duration-normal) var(--ease-default);
}
.stat-card--linked:hover::before {
  opacity: 1;
  transform: translateY(-50%) translateX(3px);
}

.stat-card--lg {
  padding: var(--space-6);
}

.stat-card--lg .stat-card__icon {
  width: 48px;
  height: 48px;
  margin-bottom: var(--space-4);
}

.stat-card--lg .stat-card__value {
  font-size: var(--text-3xl);
  font-weight: 600;
}

@media (max-width: 640px) {
  .stat-card--lg {
    padding: var(--space-5);
  }

  .stat-card--lg .stat-card__icon {
    width: 40px;
    height: 40px;
  }

  .stat-card--lg .stat-card__value {
    font-size: var(--text-2xl);
  }
}
</style>
