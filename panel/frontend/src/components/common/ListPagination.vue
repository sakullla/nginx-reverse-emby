<template>
  <div v-if="total > 0" class="list-pagination">
    <span class="list-pagination__meta">
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
  gap: 0.75rem 1rem;
  margin-top: 1rem;
  padding-top: 0.15rem;
  flex-wrap: wrap;
}

.list-pagination__meta {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
  line-height: 1.3;
}

.list-pagination__controls {
  display: flex;
  gap: 0.4rem;
}

.list-pagination__btn {
  min-height: 32px;
  padding: 0.3rem 0.7rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
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
  opacity: 0.42;
  cursor: not-allowed;
}
</style>
