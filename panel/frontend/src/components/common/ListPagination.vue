<template>
  <div v-if="total > 0" class="list-pagination">
    <span class="list-pagination__meta" role="status" aria-live="polite">
      共 {{ total }} 条 · 第 {{ page }} / {{ totalPages }} 页
    </span>
    <div class="list-pagination__controls">
      <button
        type="button"
        class="list-pagination__btn"
        :disabled="page <= 1"
        @click="emitPage(page - 1)"
      >
        上一页
      </button>
      <button
        type="button"
        class="list-pagination__btn"
        :disabled="page >= totalPages"
        @click="emitPage(page + 1)"
      >
        下一页
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 20 },
  total: { type: Number, default: 0 }
})

const emit = defineEmits(['update:page'])

const totalPages = computed(() => {
  const size = props.pageSize > 0 ? props.pageSize : 20
  return Math.max(1, Math.ceil(Math.max(0, props.total) / size))
})

function emitPage(next) {
  const page = Math.min(Math.max(1, next), totalPages.value)
  if (page !== props.page) emit('update:page', page)
}
</script>

<style scoped>
.list-pagination {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3) var(--space-4);
  margin-top: var(--space-4);
  padding-top: var(--space-0-5);
  flex-wrap: wrap;
}

.list-pagination__meta {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
  line-height: 1.3;
}

.list-pagination__controls {
  display: flex;
  gap: var(--space-1-5);
}

.list-pagination__btn {
  min-height: 32px;
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-full);
  border: var(--border-width-thin) solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  font-family: inherit;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default),
              background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.list-pagination__btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  background: var(--color-bg-hover);
  color: var(--color-primary);
}

.list-pagination__btn:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.list-pagination__btn:disabled {
  opacity: 0.48;
  cursor: not-allowed;
}

@media (max-width: 640px) {
  .list-pagination__controls {
    width: 100%;
  }

  .list-pagination__btn {
    flex: 1;
    min-height: 44px;
  }
}
</style>
