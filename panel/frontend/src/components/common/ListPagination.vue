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
  gap: 1rem;
  margin-top: 1.25rem;
  flex-wrap: wrap;
}

.list-pagination__meta {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
}

.list-pagination__controls {
  display: flex;
  gap: 0.5rem;
}

.list-pagination__btn {
  padding: 0.375rem 0.75rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  font-family: inherit;
  cursor: pointer;
}

.list-pagination__btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  background: var(--color-bg-hover);
}

.list-pagination__btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
</style>
