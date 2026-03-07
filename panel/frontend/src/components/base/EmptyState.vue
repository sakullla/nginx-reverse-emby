<template>
  <div class="empty-state-container">
    <div class="empty-state-icon">{{ icon }}</div>
    <h3 class="empty-state-title">{{ title }}</h3>
    <p class="empty-state-description">{{ description }}</p>
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
  }
})
</script>

<style scoped>
.empty-state-container {
  text-align: center;
  padding: var(--spacing-5xl) var(--spacing-xl);
  animation: fadeIn 0.5s ease-out;
}

.empty-state-icon {
  font-size: var(--font-size-4xl);
  margin-bottom: var(--spacing-xl);
  opacity: 0.5;
  animation: pulse 2s ease-in-out infinite;
}

.empty-state-title {
  font-size: var(--font-size-xl);
  font-weight: var(--font-weight-semibold);
  color: var(--color-heading);
  margin-bottom: var(--spacing-sm);
}

.empty-state-description {
  font-size: var(--font-size-base);
  color: var(--color-text-muted);
  margin-bottom: var(--spacing-xl);
  max-width: 400px;
  margin-left: auto;
  margin-right: auto;
  line-height: var(--line-height-relaxed);
}

.empty-state-action {
  margin-top: var(--spacing-xl);
}

@media (max-width: 768px) {
  .empty-state-container {
    padding: var(--spacing-3xl) var(--spacing-md);
  }

  .empty-state-icon {
    font-size: var(--font-size-3xl);
  }

  .empty-state-title {
    font-size: var(--font-size-lg);
  }

  .empty-state-description {
    font-size: var(--font-size-sm);
  }
}
</style>
