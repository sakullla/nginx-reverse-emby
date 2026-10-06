<template>
  <div
    v-if="hasItems"
    ref="rootRef"
    class="base-action-menu"
    @keydown.escape.stop="close"
  >
    <BaseIconButton
      ref="triggerRef"
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

    <!-- Keep teleported node mounted (v-show) to avoid jsdom Teleport remove races. -->
    <Teleport to="body">
      <div
        v-show="open"
        ref="panelRef"
        class="base-action-menu__panel"
        role="menu"
        data-testid="base-action-menu-panel"
        :aria-hidden="open ? 'false' : 'true'"
        :style="panelStyle"
        @keydown="handleMenuKeydown"
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
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
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
const triggerRef = ref(null)
const panelRef = ref(null)
const panelStyle = ref({})

const hasItems = computed(() => Array.isArray(props.items) && props.items.length > 0)

function triggerEl() {
  const t = triggerRef.value
  if (!t) return null
  // BaseIconButton may expose component instance; fall back to root query
  return t.$el || rootRef.value?.querySelector('.base-action-menu__trigger') || null
}

function updatePosition() {
  const el = triggerEl()
  if (!el || typeof el.getBoundingClientRect !== 'function') return
  const rect = el.getBoundingClientRect()
  const gap = 4
  const estimatedMinWidth = 136
  let left = rect.right - estimatedMinWidth
  if (left < 8) left = 8
  const maxLeft = window.innerWidth - estimatedMinWidth - 8
  if (left > maxLeft) left = Math.max(8, maxLeft)

  let top = rect.bottom + gap
  // Prefer below; if near bottom, open above after measuring panel height.
  const panel = panelRef.value
  const panelHeight = panel?.offsetHeight || 0
  if (panelHeight && top + panelHeight > window.innerHeight - 8) {
    top = Math.max(8, rect.top - gap - panelHeight)
  }

  panelStyle.value = {
    position: 'fixed',
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
    right: 'auto',
    zIndex: 1000,
  }
}

function menuItems() {
  if (!panelRef.value) return []
  return Array.from(panelRef.value.querySelectorAll('.base-action-menu__item:not(:disabled)'))
}

function focusItem(index) {
  const items = menuItems()
  if (!items.length) return
  const clamped = (index + items.length) % items.length
  items[clamped].focus()
}

function handleMenuKeydown(e) {
  const items = menuItems()
  if (!items.length) return
  const current = items.indexOf(document.activeElement)
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    focusItem(current + 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    focusItem(current <= 0 ? items.length - 1 : current - 1)
  } else if (e.key === 'Home') {
    e.preventDefault()
    focusItem(0)
  } else if (e.key === 'End') {
    e.preventDefault()
    focusItem(items.length - 1)
  } else if (e.key === 'Escape') {
    e.preventDefault()
    close()
    triggerEl()?.focus()
  }
}

async function openMenu() {
  open.value = true
  await nextTick()
  updatePosition()
  // second pass after panel paints so height-based flip is accurate
  await nextTick()
  updatePosition()
  focusItem(0)
}

function close() {
  open.value = false
}

function toggle() {
  if (open.value) close()
  else openMenu()
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
  const panel = panelRef.value
  const target = e.target
  if (root?.contains(target) || panel?.contains(target)) return
  close()
}

function handleRepositionOrClose() {
  if (!open.value) return
  // scroll/resize: close to avoid stale fixed coords mid-scroll
  close()
}

watch(
  () => props.items,
  (list) => {
    if (!Array.isArray(list) || list.length === 0) close()
  },
)

onMounted(() => {
  document.addEventListener('mousedown', handlePointerOutside)
  document.addEventListener('touchstart', handlePointerOutside)
  window.addEventListener('resize', handleRepositionOrClose)
  window.addEventListener('scroll', handleRepositionOrClose, true)
})

onUnmounted(() => {
  document.removeEventListener('mousedown', handlePointerOutside)
  document.removeEventListener('touchstart', handlePointerOutside)
  window.removeEventListener('resize', handleRepositionOrClose)
  window.removeEventListener('scroll', handleRepositionOrClose, true)
})
</script>

<style scoped>
.base-action-menu {
  position: relative;
  display: inline-flex;
  flex-shrink: 0;
}
</style>

<!-- Panel is teleported to body; keep unscoped panel styles under a stable class prefix -->
<style>
.base-action-menu__panel {
  min-width: 8.5rem;
  padding: var(--space-1);
  background: var(--color-bg-surface);
  border: var(--border-width-thin) solid var(--color-border-default);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-md);
  display: flex;
  flex-direction: column;
  gap: var(--space-0-5);
}

.base-action-menu__item {
  display: block;
  width: 100%;
  text-align: left;
  border: none;
  background: transparent;
  color: var(--color-text-primary);
  font: inherit;
  font-size: var(--text-sm);
  line-height: 1.35;
  padding: var(--space-2) var(--space-2-5);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default), color var(--duration-fast) var(--ease-default);
}

.base-action-menu__item:hover:not(:disabled),
.base-action-menu__item:focus-visible:not(:disabled) {
  background: var(--color-bg-hover);
  outline: none;
}

.base-action-menu__item:disabled {
  opacity: 0.48;
  cursor: not-allowed;
}

.base-action-menu__item--danger {
  color: var(--color-danger);
}

.base-action-menu__item--danger:hover:not(:disabled),
.base-action-menu__item--danger:focus-visible:not(:disabled) {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.base-action-menu__item--warning {
  color: var(--color-warning);
}

.base-action-menu__item--success {
  color: var(--color-success);
}

.base-action-menu__item--primary {
  color: var(--color-primary);
}
</style>
