<template>
  <div :class="['card', { 'card-hover': hover, 'card-glass': glass }]">
    <div v-if="$slots.header || title" class="card-header">
      <slot name="header">
        <h3 class="card-title">{{ title }}</h3>
      </slot>
      <div v-if="$slots.actions" class="card-actions">
        <slot name="actions" />
      </div>
    </div>
    <div class="card-body">
      <slot />
    </div>
    <div v-if="$slots.footer" class="card-footer">
      <slot name="footer" />
    </div>
  </div>
</template>

<script setup>
defineProps({
  title: {
    type: String,
    default: ''
  },
  hover: {
    type: Boolean,
    default: true
  },
  glass: {
    type: Boolean,
    default: false
  }
})
</script>

<style scoped>
.card {
  background: var(--color-bg-primary);
  border-radius: var(--radius-lg);
  padding: var(--spacing-xl);
  box-shadow: var(--shadow-md);
  transition: all var(--transition-base);
  border: 1px solid var(--color-border-light);
}

.card-hover:hover {
  box-shadow: var(--shadow-lg);
  transform: translateY(-2px);
  border-color: var(--color-border);
}

.card-glass {
  background: rgba(255, 255, 255, 0.8);
  backdrop-filter: blur(var(--blur-lg));
  -webkit-backdrop-filter: blur(var(--blur-lg));
  border: 1px solid rgba(255, 255, 255, 0.3);
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--spacing-lg);
  padding-bottom: var(--spacing-md);
  border-bottom: 2px solid var(--color-border-light);
}

.card-title {
  font-size: var(--font-size-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--color-heading);
  margin: 0;
}

.card-actions {
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
}

.card-body {
  color: var(--color-text-secondary);
  line-height: var(--line-height-relaxed);
}

.card-footer {
  margin-top: var(--spacing-lg);
  padding-top: var(--spacing-md);
  border-top: 2px solid var(--color-border-light);
}
</style>
