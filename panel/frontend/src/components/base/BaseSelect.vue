<template>
  <div ref="rootRef" class="base-select" :class="{ 'base-select--open': open }">
    <button
      ref="triggerRef"
      type="button"
      class="base-select__trigger"
      :aria-label="ariaLabel || undefined"
      aria-haspopup="listbox"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-controls="open ? listId : undefined"
      @click="toggle"
      @keydown="onTriggerKeydown"
    >
      <span class="base-select__value">{{ selectedLabel }}</span>
      <svg class="base-select__chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <polyline points="6 9 12 15 18 9" />
      </svg>
    </button>

    <Transition name="base-select">
      <ul
        v-if="open"
        :id="listId"
        ref="listRef"
        class="base-select__list"
        :class="`base-select__list--${align}`"
        role="listbox"
        tabindex="-1"
        :aria-label="ariaLabel || undefined"
        :aria-activedescendant="activeIndex >= 0 ? optionId(activeIndex) : undefined"
        @keydown="onListKeydown"
      >
        <li
          v-for="(option, index) in options"
          :id="optionId(index)"
          :key="String(option.value)"
          class="base-select__option"
          :class="{
            'base-select__option--active': index === activeIndex,
            'base-select__option--selected': isSelected(option)
          }"
          role="option"
          :aria-selected="isSelected(option) ? 'true' : 'false'"
          @mousemove="activeIndex = index"
          @click="choose(option)"
        >
          <span class="base-select__option-label">{{ option.label }}</span>
          <svg
            v-if="isSelected(option)"
            class="base-select__check"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2.5"
            aria-hidden="true"
          >
            <polyline points="20 6 9 17 4 12" />
          </svg>
        </li>
      </ul>
    </Transition>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'

const props = defineProps({
  modelValue: { type: [String, Number], default: '' },
  /** [{ value, label }] */
  options: { type: Array, default: () => [] },
  ariaLabel: { type: String, default: '' },
  placeholder: { type: String, default: '请选择' },
  /** Which trigger edge the option list lines up with. */
  align: { type: String, default: 'start', validator: (value) => ['start', 'end'].includes(value) }
})

const emit = defineEmits(['update:modelValue'])

const open = ref(false)
const activeIndex = ref(-1)
const rootRef = ref(null)
const triggerRef = ref(null)
const listRef = ref(null)

const instanceId = Math.random().toString(36).slice(2, 8)
const listId = `base-select-${instanceId}`

function optionId(index) {
  return `${listId}-option-${index}`
}

function isSelected(option) {
  return String(option.value) === String(props.modelValue)
}

const selectedIndex = computed(() => props.options.findIndex(isSelected))

const selectedLabel = computed(() => {
  const hit = props.options[selectedIndex.value]
  return hit ? hit.label : props.placeholder
})

async function openList() {
  if (!props.options.length) return
  open.value = true
  activeIndex.value = selectedIndex.value >= 0 ? selectedIndex.value : 0
  await nextTick()
  listRef.value?.focus?.()
  scrollActiveIntoView()
}

function closeList({ focusTrigger = false } = {}) {
  if (!open.value) return
  open.value = false
  activeIndex.value = -1
  if (focusTrigger) triggerRef.value?.focus?.()
}

function toggle() {
  if (open.value) closeList()
  else openList()
}

function choose(option) {
  if (!isSelected(option)) emit('update:modelValue', option.value)
  closeList({ focusTrigger: true })
}

function scrollActiveIntoView() {
  const node = listRef.value?.querySelector?.(`#${optionId(activeIndex.value)}`)
  node?.scrollIntoView?.({ block: 'nearest' })
}

function moveActive(next) {
  const count = props.options.length
  if (!count) return
  activeIndex.value = Math.min(Math.max(next, 0), count - 1)
  nextTick(scrollActiveIntoView)
}

function onTriggerKeydown(event) {
  if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) {
    event.preventDefault()
    if (!open.value) openList()
  }
}

function onListKeydown(event) {
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      moveActive(activeIndex.value + 1)
      break
    case 'ArrowUp':
      event.preventDefault()
      moveActive(activeIndex.value - 1)
      break
    case 'Home':
      event.preventDefault()
      moveActive(0)
      break
    case 'End':
      event.preventDefault()
      moveActive(props.options.length - 1)
      break
    case 'Enter':
    case ' ': {
      event.preventDefault()
      const option = props.options[activeIndex.value]
      if (option) choose(option)
      break
    }
    case 'Escape':
      event.preventDefault()
      closeList({ focusTrigger: true })
      break
    case 'Tab':
      closeList()
      break
    default:
      break
  }
}

function handleClickOutside(event) {
  if (open.value && rootRef.value && !rootRef.value.contains(event.target)) closeList()
}

onMounted(() => document.addEventListener('mousedown', handleClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', handleClickOutside))
</script>

<style scoped>
.base-select {
  position: relative;
  display: inline-flex;
  min-width: 0;
}

.base-select__trigger {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 34px;
  padding: var(--space-1-5) var(--space-2-5) var(--space-1-5) var(--space-3);
  border-radius: var(--radius-lg);
  border: var(--border-width-thick) solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  font-family: inherit;
  cursor: pointer;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default),
              background var(--duration-fast) var(--ease-default);
}

.base-select__trigger:hover {
  border-color: var(--color-primary);
  background: var(--color-bg-hover);
}

.base-select__trigger:focus-visible,
.base-select--open .base-select__trigger {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.base-select__value {
  flex: 1;
  min-width: 0;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.base-select__chevron {
  flex-shrink: 0;
  color: var(--color-text-muted);
  transition: transform var(--duration-fast) var(--ease-default);
}

.base-select--open .base-select__chevron {
  transform: rotate(180deg);
}

.base-select__list {
  position: absolute;
  top: calc(100% + var(--space-1-5));
  z-index: var(--z-dropdown);
  min-width: 100%;
  max-height: 16rem;
  margin: 0;
  padding: var(--space-1);
  list-style: none;
  overflow-y: auto;
  overscroll-behavior: contain;
  background: var(--color-bg-surface-raised);
  border: var(--border-width-thick) solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  box-sizing: border-box;
  outline: none;
}

.base-select__list--start {
  left: 0;
}

.base-select__list--end {
  right: 0;
}

.base-select__option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 34px;
  padding: var(--space-1-5) var(--space-2-5);
  border-radius: var(--radius-md);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  white-space: nowrap;
  cursor: pointer;
  box-sizing: border-box;
}

.base-select__option--active {
  background: var(--color-bg-hover);
}

.base-select__option--selected {
  color: var(--color-primary);
  font-weight: var(--font-semibold);
}

.base-select__option-label {
  flex: 1;
}

.base-select__check {
  flex-shrink: 0;
}

.base-select-enter-active,
.base-select-leave-active {
  transition: opacity var(--duration-fast) var(--ease-default),
              transform var(--duration-fast) var(--ease-default);
}

.base-select-enter-from,
.base-select-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

@media (prefers-reduced-motion: reduce) {
  .base-select-enter-active,
  .base-select-leave-active {
    transition: none;
  }
}

@media (max-width: 768px) {
  .base-select__trigger,
  .base-select__option {
    min-height: 44px;
  }
}
</style>
