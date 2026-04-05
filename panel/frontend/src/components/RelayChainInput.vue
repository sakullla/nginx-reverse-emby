<template>
  <div class='relay-chain'>
    <div class='relay-chain__add'>
      <select v-model='pendingId' class='input' :disabled='disabled || !availableOptions.length'>
        <option value=''>选择 Relay 监听器...</option>
        <option v-for='listener in availableOptions' :key='listener.id' :value='listener.id'>
          {{ formatListener(listener) }}
        </option>
      </select>
      <button type='button' class='btn btn--secondary btn--sm' :disabled='disabled || !pendingId' @click='addSelected'>添加</button>
    </div>

    <p v-if='!listeners.length' class='relay-chain__hint'>暂无可用 Relay 监听器</p>

    <ul v-if='selectedListeners.length' class='relay-chain__list'>
      <li v-for='(listener, index) in selectedListeners' :key='listener.id' class='relay-chain__item'>
        <span class='relay-chain__item-label'>{{ formatListener(listener) }}</span>
        <div class='relay-chain__item-actions'>
          <button type='button' class='btn btn--icon' :disabled='disabled || index === 0' @click='moveUp(index)'>↑</button>
          <button type='button' class='btn btn--icon' :disabled='disabled || index === selectedListeners.length - 1' @click='moveDown(index)'>↓</button>
          <button type='button' class='btn btn--icon btn--danger-ghost' :disabled='disabled' @click='removeAt(index)'>✕</button>
        </div>
      </li>
    </ul>

    <p v-else class='relay-chain__hint'>未配置 Relay 链路（直连）</p>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  listeners: { type: Array, default: () => [] },
  disabled: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue'])

const pendingId = ref('')

const selectedIds = computed(() => (props.modelValue || [])
  .map((id) => Number(id))
  .filter((id) => Number.isInteger(id) && id > 0))

const availableOptions = computed(() => {
  const selected = new Set(selectedIds.value)
  return (props.listeners || []).filter((listener) => !selected.has(Number(listener.id)))
})

const selectedListeners = computed(() => {
  const map = new Map((props.listeners || []).map((listener) => [Number(listener.id), listener]))
  return selectedIds.value
    .map((id) => map.get(id) || { id, name: `监听器 ${id}` })
})

function formatListener(listener) {
  const name = listener?.name || `监听器 ${listener?.id}`
  const host = listener?.listen_host || '0.0.0.0'
  const port = listener?.listen_port || '-'
  return `${name} (${host}:${port})`
}

function updateChain(next) {
  emit('update:modelValue', next)
}

function addSelected() {
  const nextId = Number(pendingId.value)
  if (!Number.isInteger(nextId) || nextId <= 0) return
  updateChain([...selectedIds.value, nextId])
  pendingId.value = ''
}

function moveUp(index) {
  if (index <= 0) return
  const next = [...selectedIds.value]
  const current = next[index]
  next[index] = next[index - 1]
  next[index - 1] = current
  updateChain(next)
}

function moveDown(index) {
  if (index >= selectedIds.value.length - 1) return
  const next = [...selectedIds.value]
  const current = next[index]
  next[index] = next[index + 1]
  next[index + 1] = current
  updateChain(next)
}

function removeAt(index) {
  const next = [...selectedIds.value]
  next.splice(index, 1)
  updateChain(next)
}
</script>

<style scoped>
.relay-chain {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.relay-chain__add {
  display: flex;
  gap: var(--space-2);
}

.relay-chain__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.relay-chain__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
}

.relay-chain__item-label {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
}

.relay-chain__item-actions {
  display: flex;
  gap: var(--space-1);
}

.relay-chain__hint {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.input {
  width: 100%;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
}

.btn {
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  cursor: pointer;
}

.btn--sm {
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-xs);
}

.btn--icon {
  width: 24px;
  height: 24px;
}

.btn--secondary {
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
}

.btn--danger-ghost {
  color: var(--color-danger);
}
</style>
