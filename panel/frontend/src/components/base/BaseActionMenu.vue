<template>
  <div
    v-if="hasItems"
    ref="rootRef"
    class="base-action-menu"
    @keydown.escape.stop="close"
  >
    <BaseIconButton
      class="base-action-menu__trigger"
      title="更多操作"
      :aria-expanded="open ? 'true' : 'false'"
      aria-haspopup="menu"
      @click="toggle"
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <circle cx="5" cy="12" r="2" />
        <circle cx="12" cy="12" r="2" />
        <circle cx="19" cy="12" r="2" />
      </svg>
    </BaseIconButton>

    <div
      v-if="open"
      class="base-action-menu__panel"
      role="menu"
      data-testid="base-action-menu-panel"
    >
      <button
        v-for="item in items"
        :key="item.id"
        type="button"
        role="menuitem"
        class="base-action-menu__item"
        :class="itemClass(item)"
        :disabled="!!item.disabled"
        :data-testid="`base-action-menu-item-${item.id}`"
        @click="onSelect(item)"
      >
        {{ item.label }}
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import BaseIconButton from './BaseIconButton.vue'

const props = defineProps({
  items: {
    type: Array,
    default: () => [],
    validator: (list) =>
      Array.isArray(list) &&
      list.every(
        (item) =>
          item &&
          typeof item === 'object' &&
          (typeof item.id === 'string' || typeof item.id === 'number') &&
          typeof item.label === 'string',
      ),
  },
})

const emit = defineEmits(['select'])

const open = ref(false)
const rootRef = ref(null)

const hasItems = computed(() => Array.isArray(props.items) && props.items.length > 0)

function toggle() {
  open.value = !open.value
}

function close() {
  open.value = false
}

function itemClass(item) {
  const tone = item.tone || 'default'
  return {
    [`base-action-menu__item--${tone}`]: true,
  }
}

function onSelect(item) {
  if (item.disabled) return
  emit('select', item)
  close()
}

function handlePointerOutside(e) {
  if (!open.value) return
  const root = rootRef.value
  if (root && !root.contains(e.target)) {
    close()
  }
}

onMounted(() => {
  document.addEventListener('mousedown', handlePointerOutside)
  document.addEventListener('touchstart', handlePointerOutside)
})

onUnmounted(() => {
  document.removeEventListener('mousedown', handlePointerOutside)
  document.removeEventListener('touchstart', handlePointerOutside)
})
</script>

<style scoped>
.base-action-menu {
  position: relative;
  display: inline-flex;
  flex-shrink: 0;
}

.base-action-menu__panel {
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  z-index: 20;
  min-width: 8.5rem;
  padding: 0.25rem;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg, 0.5rem);
  box-shadow: var(--shadow-md);
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
}

.base-action-menu__item {
  display: block;
  width: 100%;
  text-align: left;
  border: none;
  background: transparent;
  color: var(--color-text-primary);
  font: inherit;
  font-size: var(--text-sm, 0.875rem);
  line-height: 1.35;
  padding: 0.5rem 0.625rem;
  border-radius: var(--radius-md, 0.375rem);
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.base-action-menu__item:hover:not(:disabled),
.base-action-menu__item:focus-visible:not(:disabled) {
  background: var(--color-bg-hover);
  outline: none;
}

.base-action-menu__item:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.base-action-menu__item--danger {
  color: var(--color-danger, #dc2626);
}

.base-action-menu__item--danger:hover:not(:disabled),
.base-action-menu__item--danger:focus-visible:not(:disabled) {
  background: var(--color-danger-50, rgba(220, 38, 38, 0.08));
  color: var(--color-danger, #dc2626);
}

.base-action-menu__item--warning {
  color: var(--color-warning, #d97706);
}

.base-action-menu__item--success {
  color: var(--color-success, #059669);
}

.base-action-menu__item--primary {
  color: var(--color-primary);
}
</style>
