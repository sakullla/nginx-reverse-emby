<template>
  <div
    class="empty-state-container"
    :class="{ 'empty-state-container--compact': compact }"
    role="status"
  >
    <div class="empty-state-icon" aria-hidden="true">
      <slot name="icon">{{ icon }}</slot>
    </div>
    <h3 class="empty-state-title">{{ title }}</h3>
    <p v-if="description" class="empty-state-description">{{ description }}</p>
    <div v-if="$slots.action" class="empty-state-action">
      <slot name="action" />
    </div>
  </div>
</template>

<script setup>
defineProps({
  icon: {
    type: String,
    default: '📋'
  },
  title: {
    type: String,
    default: '暂无数据'
  },
  description: {
    type: String,
    default: '还没有任何内容,快来添加第一条吧!'
  },
  compact: {
    type: Boolean,
    default: false
  }
})
</script>

<style scoped>
.empty-state-container {
  text-align: center;
  padding: var(--space-16) var(--space-6);
  animation: fadeIn 0.5s ease-out;
}

.empty-state-container--compact {
  padding: var(--space-8) var(--space-4);
}

.empty-state-icon {
  font-size: var(--text-3xl);
  margin-bottom: var(--space-6);
  opacity: 0.5;
  color: var(--color-text-muted);
}

.empty-state-container--compact .empty-state-icon {
  margin-bottom: var(--space-3);
}

.empty-state-title {
  font-size: var(--text-xl);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin-bottom: var(--space-2);
}

.empty-state-container--compact .empty-state-title {
  font-size: var(--text-base);
}

.empty-state-description {
  font-size: var(--text-base);
  color: var(--color-text-muted);
  margin-bottom: var(--space-6);
  max-width: 400px;
  margin-left: auto;
  margin-right: auto;
  line-height: 1.625;
}

.empty-state-container--compact .empty-state-description {
  font-size: var(--text-sm);
  margin-bottom: var(--space-4);
}

.empty-state-action {
  margin-top: var(--space-6);
}

.empty-state-container--compact .empty-state-action {
  margin-top: var(--space-4);
}

@media (max-width: 768px) {
  .empty-state-container {
    padding: var(--space-12) var(--space-4);
  }

  .empty-state-icon {
    font-size: var(--text-2xl);
  }

  .empty-state-title {
    font-size: var(--text-lg);
  }

  .empty-state-description {
    font-size: var(--text-sm);
  }
}

@media (prefers-reduced-motion: reduce) {
  .empty-state-container {
    animation: none;
  }
}
</style>
