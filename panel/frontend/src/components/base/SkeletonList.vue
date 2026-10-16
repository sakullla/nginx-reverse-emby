<template>
  <div
    class="skeleton-list"
    :class="`skeleton-list--${variant}`"
    role="status"
    :aria-label="label"
  >
    <span class="visually-hidden">{{ label }}</span>
    <div
      v-for="i in count"
      :key="i"
      class="skeleton-list__item"
      :style="{ animationDelay: `${(i - 1) * 60}ms` }"
      aria-hidden="true"
    >
      <template v-if="variant === 'cards'">
        <div class="skeleton skeleton-list__card-head"></div>
        <div class="skeleton skeleton-list__line skeleton-list__line--title"></div>
        <div class="skeleton skeleton-list__line"></div>
        <div class="skeleton skeleton-list__line skeleton-list__line--short"></div>
      </template>
      <template v-else>
        <div class="skeleton skeleton-list__avatar"></div>
        <div class="skeleton-list__lines">
          <div class="skeleton skeleton-list__line skeleton-list__line--title"></div>
          <div class="skeleton skeleton-list__line"></div>
        </div>
        <div class="skeleton skeleton-list__tail"></div>
      </template>
    </div>
  </div>
</template>

<script setup>
defineProps({
  variant: {
    type: String,
    default: 'rows',
    validator: (v) => ['rows', 'cards'].includes(v)
  },
  count: {
    type: Number,
    default: 5
  },
  label: {
    type: String,
    default: '内容加载中'
  }
})
</script>

<style scoped>
.skeleton-list {
  display: flex;
  flex-direction: column;
}

/* Row variant — resembles a list table wrapped in a surface card */
.skeleton-list--rows {
  background: var(--color-bg-surface);
  border: var(--border-width-thin) solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  overflow: hidden;
}

.skeleton-list--rows .skeleton-list__item {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
}

.skeleton-list--rows .skeleton-list__item:last-child {
  border-bottom: 0;
}

.skeleton-list__avatar {
  width: 2.25rem;
  height: 2.25rem;
  border-radius: var(--radius-lg);
  flex-shrink: 0;
}

.skeleton-list__lines {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.skeleton-list__line {
  height: var(--space-2-5);
  border-radius: var(--radius-sm);
}

.skeleton-list__line--title {
  width: min(55%, 22rem);
  height: var(--space-3);
}

.skeleton-list--rows .skeleton-list__lines .skeleton-list__line:not(.skeleton-list__line--title) {
  width: min(80%, 30rem);
}

.skeleton-list__tail {
  width: 4.5rem;
  height: var(--space-6);
  border-radius: var(--radius-md);
  flex-shrink: 0;
}

/* Card variant — mirrors .card-grid so the swap to real cards is seamless */
.skeleton-list--cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: var(--space-6);
}

.skeleton-list--cards .skeleton-list__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5) var(--space-5);
  background: var(--color-bg-surface);
  border: var(--border-width-thin) solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
}

.skeleton-list__card-head {
  width: 38%;
  height: var(--text-sm);
  border-radius: var(--radius-sm);
}

.skeleton-list--cards .skeleton-list__line:not(.skeleton-list__line--title) {
  width: 90%;
}

.skeleton-list__line--short {
  width: 60%;
}

@media (min-width: 1280px) {
  .skeleton-list--cards {
    grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
  }
}

@media (max-width: 640px) {
  .skeleton-list--cards {
    grid-template-columns: 1fr;
    gap: var(--space-3);
  }
}
</style>
