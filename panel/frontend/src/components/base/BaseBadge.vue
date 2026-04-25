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
    <i v-if="dot" class="base-badge__dot" />
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
  gap: 4px;
  line-height: 1;
  font-weight: 600;
  white-space: nowrap;
  flex-shrink: 0;
}

.base-badge__dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}

/* Tones — backgrounds + foregrounds map onto themes.css tokens */
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

/* Shapes */
.base-badge--pill {
  border-radius: var(--radius-full);
}
.base-badge--square {
  border-radius: var(--radius-sm);
}

/* Sizes */
.base-badge--sm {
  font-size: 0.7rem;
  padding: 2px 6px;
  font-weight: 700;
}
.base-badge--md {
  font-size: 0.75rem;
  padding: 2px 8px;
}

/* Mono — for protocol / scope / tuning chips */
.base-badge--mono {
  font-family: var(--font-mono);
  font-weight: 700;
  letter-spacing: 0.02em;
}
</style>
