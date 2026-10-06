<template>
  <span
    class="base-badge"
    :class="[
      `base-badge--${tone}`,
      tone === 'neutral' ? `base-badge--${subtone}` : null,
      `base-badge--${shape}`,
      `base-badge--${size}`,
      mono ? 'base-badge--mono' : null,
    ]"
  >
    <i v-if="dot" class="base-badge__dot" aria-hidden="true" />
    <slot />
  </span>
</template>

<script setup>
defineProps({
  tone: {
    type: String,
    default: 'neutral',
    validator: (v) => ['success', 'warning', 'danger', 'primary', 'neutral'].includes(v),
  },
  subtone: {
    type: String,
    default: 'muted',
    validator: (v) => ['muted', 'secondary'].includes(v),
  },
  shape: {
    type: String,
    default: 'pill',
    validator: (v) => ['pill', 'square'].includes(v),
  },
  size: {
    type: String,
    default: 'sm',
    validator: (v) => ['sm', 'md'].includes(v),
  },
  mono: { type: Boolean, default: false },
  dot: { type: Boolean, default: false },
})
</script>

<style scoped>
.base-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  line-height: 1;
  font-weight: var(--font-semibold);
  white-space: nowrap;
  flex-shrink: 0;
  transition: all var(--duration-fast) var(--ease-default);
}

.base-badge__dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}

.base-badge--success {
  background: var(--color-success-50);
  color: var(--color-success);
}

.base-badge--warning {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.base-badge--danger {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.base-badge--primary {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.base-badge--neutral {
  background: var(--color-bg-subtle);
}

.base-badge--neutral.base-badge--muted {
  color: var(--color-text-muted);
}

.base-badge--neutral.base-badge--secondary {
  color: var(--color-text-secondary);
}

.base-badge--pill {
  border-radius: var(--radius-full);
}

.base-badge--square {
  border-radius: var(--radius-sm);
}

.base-badge--sm {
  font-size: var(--text-xs);
  padding: var(--space-0-5) var(--space-1-5);
  font-weight: var(--font-bold);
}

.base-badge--md {
  font-size: var(--text-xs);
  padding: var(--space-1) var(--space-2);
}

.base-badge--mono {
  font-family: var(--font-mono);
  font-weight: var(--font-bold);
  letter-spacing: 0.02em;
}
</style>
