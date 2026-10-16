<template>
  <p
    :id="id || undefined"
    class="form-error"
    :class="{ 'form-error--block': block }"
    role="alert"
  >
    <svg
      class="form-error__icon"
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
    <span class="form-error__text"><slot /></span>
  </p>
</template>

<script setup>
defineProps({
  id: {
    type: String,
    default: ''
  },
  block: {
    type: Boolean,
    default: false
  }
})
</script>

<style scoped>
.form-error {
  display: flex;
  align-items: flex-start;
  gap: var(--space-1);
  margin: 0;
  font-size: var(--text-xs);
  line-height: 1.4;
  color: var(--color-danger);
}

.form-error__icon {
  flex-shrink: 0;
  margin-top: 0.1em;
}

.form-error__text {
  min-width: 0;
}

.form-error--block {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-danger-50);
}

.form-error--block .form-error__icon {
  margin-top: 0.15em;
}
</style>
