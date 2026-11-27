<template>
  <div
    class="error-state-container"
    :class="{ 'error-state-container--compact': compact }"
    role="alert"
  >
    <div class="error-state-icon" aria-hidden="true">
      <slot name="icon">
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="9" />
          <path d="M12 7.5v5" />
          <path d="M12 16.25h.01" />
        </svg>
      </slot>
    </div>
    <h3 class="error-state-title">{{ title }}</h3>
    <p v-if="message" class="error-state-message">{{ message }}</p>
    <div v-if="showRetry || $slots.action" class="error-state-action">
      <slot name="action">
        <BaseButton
          variant="secondary"
          size="sm"
          :loading="retrying"
          :disabled="retrying"
          @click="$emit('retry')"
        >
          {{ retryLabel }}
        </BaseButton>
      </slot>
    </div>
  </div>
</template>

<script setup>
import BaseButton from './BaseButton.vue'

defineProps({
  title: { type: String, default: '加载失败' },
  message: { type: String, default: '' },
  retrying: { type: Boolean, default: false },
  retryLabel: { type: String, default: '重试' },
  showRetry: { type: Boolean, default: true },
  compact: { type: Boolean, default: false }
})

defineEmits(['retry'])
</script>

<style scoped>
/* Same footprint as EmptyState so loading/empty/error swap without layout jumps. */
.error-state-container {
  text-align: center;
  padding: var(--space-16) var(--space-6);
}

.error-state-container--compact {
  padding: var(--space-8) var(--space-4);
}

.error-state-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 64px;
  margin-bottom: var(--space-5);
  border-radius: var(--radius-2xl);
  border: 1px solid color-mix(in srgb, var(--color-danger) 30%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-danger) 8%, var(--color-bg-surface));
  box-shadow: var(--shadow-sm);
  color: var(--color-danger);
}

.error-state-container--compact .error-state-icon {
  margin-bottom: var(--space-3);
}

.error-state-title {
  font-size: var(--text-lg);
  letter-spacing: -0.02em;
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin-bottom: var(--space-2);
}

.error-state-container--compact .error-state-title {
  font-size: var(--text-base);
}

.error-state-message {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  max-width: 440px;
  margin: 0 auto;
  line-height: 1.625;
  overflow-wrap: anywhere;
}

.error-state-action {
  margin-top: var(--space-6);
  display: flex;
  justify-content: center;
  gap: var(--space-2);
}

.error-state-container--compact .error-state-action {
  margin-top: var(--space-4);
}

@media (max-width: 768px) {
  .error-state-container {
    padding: var(--space-12) var(--space-4);
  }
}
</style>
