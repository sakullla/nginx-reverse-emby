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
  gap: 0.3rem;
  line-height: 1;
  font-weight: var(--font-medium);
  white-space: nowrap;
  flex-shrink: 0;
  box-sizing: border-box;
  border: 1px solid transparent;
  transition: background var(--duration-fast) var(--ease-default), color var(--duration-fast) var(--ease-default);
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
  border-color: color-mix(in srgb, var(--color-success) 16%, transparent);
  color: var(--color-success);
}

.base-badge--warning {
  background: var(--color-warning-50);
  border-color: color-mix(in srgb, var(--color-warning) 18%, transparent);
  color: var(--color-warning);
}

.base-badge--danger {
  background: var(--color-danger-50);
  border-color: color-mix(in srgb, var(--color-danger) 16%, transparent);
  color: var(--color-danger);
}

.base-badge--primary {
  background: var(--color-primary-50);
  border-color: color-mix(in srgb, var(--color-primary) 18%, transparent);
  color: var(--color-primary);
}

.base-badge--neutral {
  background: var(--color-bg-subtle);
  border-color: var(--color-border-default);
}

.base-badge--neutral.base-badge--muted {
  color: var(--color-text-tertiary);
}

.base-badge--neutral.base-badge--secondary {
  color: var(--color-text-secondary);
}

.base-badge--pill {
  border-radius: var(--radius-full);
}

.base-badge--square {
  border-radius: var(--radius-xs);
}

.base-badge--sm {
  height: 1.25rem;
  font-size: var(--text-2xs);
  padding: 0 var(--space-1-5);
}

.base-badge--md {
  height: 1.5rem;
  font-size: var(--text-xs);
  padding: 0 var(--space-2);
}

.base-badge--mono {
  font-family: var(--font-mono);
  font-weight: var(--font-medium);
  letter-spacing: 0;
}

.base-badge.tag-badge {
  border-radius: var(--radius-xs);
}
</style>
