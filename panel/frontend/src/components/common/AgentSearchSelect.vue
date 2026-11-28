<template>
  <div
    class="agent-search-select"
    :class="{ 'agent-search-select--form': !showAllOption }"
    ref="rootRef"
  >
    <button
      ref="triggerRef"
      :id="id || undefined"
      type="button"
      class="agent-search-select__trigger"
      :class="{
        'agent-search-select__trigger--open': open,
        'agent-search-select__trigger--invalid': invalid,
        'agent-search-select__trigger--disabled': disabled,
      }"
      :disabled="disabled"
      :aria-expanded="open ? 'true' : 'false'"
      aria-haspopup="listbox"
      :aria-controls="open ? listboxId : undefined"
      :aria-invalid="invalid ? 'true' : undefined"
      :aria-label="ariaLabel || undefined"
      :aria-describedby="ariaDescribedby || undefined"
      @click="toggleOpen"
      @keydown="onTriggerKeydown"
    >
      <span
        v-if="selectedAgent"
        class="agent-search-select__status-dot"
        :class="`agent-search-select__status-dot--${getAgentStatus(selectedAgent)}`"
      />
      <span
        class="agent-search-select__label"
        :class="{
          'agent-search-select__label--placeholder': isPlaceholder,
          'agent-search-select__label--missing': isMissingSelection,
        }"
      >{{ selectedLabel }}</span>
      <svg
        class="agent-search-select__chevron"
        :class="{ 'agent-search-select__chevron--open': open }"
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        aria-hidden="true"
      >
        <polyline points="6 9 12 15 18 9" />
      </svg>
    </button>

    <Teleport to="body">
      <div
        v-if="open"
        ref="dropdownRef"
        :id="listboxId"
        class="agent-search-select__dropdown"
        :class="{ 'agent-search-select__dropdown--above': placedAbove }"
        role="listbox"
        :style="dropdownStyle"
        @keydown="onDropdownKeydown"
      >
        <div class="agent-search-select__search">
          <div class="agent-search-select__search-shell">
            <svg
              class="agent-search-select__search-icon"
              width="13"
              height="13"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              aria-hidden="true"
            >
              <circle cx="11" cy="11" r="7" />
              <path d="M20 20l-3.5-3.5" />
            </svg>
            <input
              ref="searchInputRef"
              v-model="searchQuery"
              type="search"
              class="agent-search-select__search-input"
              placeholder="搜索节点..."
              aria-label="搜索节点"
              :aria-activedescendant="activeOptionId || undefined"
            />
            <button
              v-if="hasSearchQuery"
              type="button"
              class="agent-search-select__search-clear"
              aria-label="清空搜索"
              title="清空搜索"
              @click="clearSearch"
            >
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" aria-hidden="true">
                <path d="M18 6L6 18M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>

        <div class="agent-search-select__filters">
          <button
            v-for="opt in statusOptions"
            :key="opt.value"
            type="button"
            class="agent-search-select__filter-btn"
            :class="`agent-search-select__filter-btn--${opt.value || 'all'} ${statusFilter === opt.value ? 'agent-search-select__filter-btn--active' : ''}`"
            @click="statusFilter = opt.value"
          >
            {{ opt.label }}
          </button>
        </div>

        <div ref="listRef" class="agent-search-select__list">
          <button
            v-for="(option, index) in selectableOptions"
            :key="option.key"
            :id="optionId(index)"
            type="button"
            class="agent-search-select__option"
            :class="{
              'agent-search-select__option--active': isOptionSelected(option),
              'agent-search-select__option--highlighted': highlightedIndex === index,
            }"
            role="option"
            :aria-selected="isOptionSelected(option) ? 'true' : 'false'"
            :data-agent-id="option.kind === 'agent' ? option.id : undefined"
            @mouseenter="highlightedIndex = index"
            @click="selectOption(option)"
          >
            <template v-if="option.kind === 'all'">
              <span class="agent-search-select__option-name">全部节点</span>
            </template>
            <template v-else>
              <span
                class="agent-search-select__status-dot"
                :class="`agent-search-select__status-dot--${getAgentStatus(option.agent)}`"
              />
              <span class="agent-search-select__option-name">{{ option.agent.name || option.agent.id }}</span>
              <span class="agent-search-select__option-time">{{ timeAgo(option.agent.last_seen_at) }}</span>
            </template>
          </button>

          <div v-if="!displayedAgents.length" class="agent-search-select__empty">
            没有匹配的节点
          </div>
        </div>

        <div class="agent-search-select__sort">
          <span>排序:</span>
          <button
            type="button"
            class="agent-search-select__sort-btn"
            :class="{ 'agent-search-select__sort-btn--active': sortBy === 'last_seen', 'agent-search-select__sort-btn--last_seen': true }"
            @click="sortBy = 'last_seen'"
          >
            最近活跃
          </button>
          <button
            type="button"
            class="agent-search-select__sort-btn"
            :class="{ 'agent-search-select__sort-btn--active': sortBy === 'name', 'agent-search-select__sort-btn--name': true }"
            @click="sortBy = 'name'"
          >
            名称
          </button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import { getAgentStatus, timeAgo } from '../../utils/agentHelpers.js'
import { ALL_AGENTS_FILTER, isAllAgentsFilter, normalizeAgentFilter } from '../../utils/agentFilter.js'

const props = defineProps({
  modelValue: { type: String, default: ALL_AGENTS_FILTER },
  agents: { type: Array, default: () => [] },
  showAllOption: { type: Boolean, default: true },
  placeholder: { type: String, default: '选择节点' },
  missingLabel: { type: String, default: '不存在的节点' },
  id: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
  invalid: { type: Boolean, default: false },
  ariaLabel: { type: String, default: '' },
  ariaDescribedby: { type: String, default: '' },
})

const emit = defineEmits(['update:modelValue'])

const open = ref(false)
const searchQuery = ref('')
const statusFilter = ref('')
const sortBy = ref('last_seen')
const highlightedIndex = ref(-1)
const placedAbove = ref(false)
const dropdownStyle = ref({})

const rootRef = ref(null)
const triggerRef = ref(null)
const dropdownRef = ref(null)
const listRef = ref(null)
const searchInputRef = ref(null)

const listboxId = `agent-search-listbox-${useId()}`

const statusOptions = [
  { value: '', label: '全部' },
  { value: 'online', label: '在线' },
  { value: 'offline', label: '离线' }
]

const isAllSelected = computed(() => {
  if (!props.showAllOption) return false
  const normalized = normalizeAgentFilter(props.modelValue)
  return !normalized || isAllAgentsFilter(normalized)
})

const normalizedSelectedId = computed(() => {
  if (props.showAllOption) {
    if (isAllSelected.value) return ''
    return normalizeAgentFilter(props.modelValue) || ''
  }
  const raw = props.modelValue == null ? '' : String(props.modelValue).trim()
  if (!raw || isAllAgentsFilter(raw)) return ''
  return raw
})

const selectedAgent = computed(() => {
  if (!normalizedSelectedId.value) return null
  return (props.agents || []).find((agent) => String(agent?.id ?? '') === normalizedSelectedId.value) || null
})

const isPlaceholder = computed(() => !props.showAllOption && !normalizedSelectedId.value)

const isMissingSelection = computed(() => (
  !props.showAllOption && Boolean(normalizedSelectedId.value) && !selectedAgent.value
))

const selectedLabel = computed(() => {
  if (props.showAllOption) {
    if (isAllSelected.value) return '全部节点'
    if (selectedAgent.value) return selectedAgent.value.name || selectedAgent.value.id
    return normalizedSelectedId.value || '全部节点'
  }
  if (selectedAgent.value) return selectedAgent.value.name || selectedAgent.value.id
  if (isMissingSelection.value) return props.missingLabel || '不存在的节点'
  return props.placeholder || '选择节点'
})

const hasSearchQuery = computed(() => String(searchQuery.value || '').length > 0)

const displayedAgents = computed(() => {
  let result = [...(props.agents || [])]

  if (statusFilter.value) {
    result = result.filter((agent) => getAgentStatus(agent) === statusFilter.value)
  }

  const q = searchQuery.value.trim().toLowerCase()
  if (q) {
    result = result.filter((agent) => (
      String(agent?.name || '').toLowerCase().includes(q)
      || String(agent?.id || '').toLowerCase().includes(q)
    ))
  }

  result.sort((a, b) => {
    if (sortBy.value === 'name') {
      return String(a?.name || a?.id || '').localeCompare(String(b?.name || b?.id || ''))
    }
    return new Date(b?.last_seen_at || 0) - new Date(a?.last_seen_at || 0)
  })

  return result
})

const selectableOptions = computed(() => {
  const items = []
  if (props.showAllOption) {
    items.push({ kind: 'all', key: '__all__', id: ALL_AGENTS_FILTER })
  }
  for (const agent of displayedAgents.value) {
    if (!agent?.id) continue
    items.push({ kind: 'agent', key: `agent:${agent.id}`, id: String(agent.id), agent })
  }
  return items
})

function optionId(index) {
  return `${listboxId}-opt-${index}`
}

const activeOptionId = computed(() => (
  highlightedIndex.value >= 0 && highlightedIndex.value < selectableOptions.value.length
    ? optionId(highlightedIndex.value)
    : ''
))

function isOptionSelected(option) {
  if (option.kind === 'all') return isAllSelected.value
  return !isAllSelected.value && String(option.id) === normalizedSelectedId.value
}

function clamp(value, min, max) {
  return Math.min(Math.max(value, min), max)
}

function updateDropdownPosition() {
  if (!open.value || !triggerRef.value) return
  const rect = triggerRef.value.getBoundingClientRect()
  const viewportWidth = window.innerWidth || 1024
  const viewportHeight = window.innerHeight || 768
  const margin = 8
  const gap = 6
  const maxWidth = Math.max(200, viewportWidth - margin * 2)
  const width = Math.min(Math.max(rect.width || 0, 320), maxWidth)
  const maxLeft = Math.max(margin, viewportWidth - margin - width)
  const left = clamp(rect.left, margin, maxLeft)

  const dropdownEl = dropdownRef.value
  const measuredHeight = dropdownEl?.getBoundingClientRect?.().height || dropdownEl?.offsetHeight || 0
  const estimatedHeight = measuredHeight > 0 ? measuredHeight : 320

  const spaceBelow = viewportHeight - rect.bottom - gap - margin
  const spaceAbove = rect.top - gap - margin
  const placeAbove = spaceBelow < Math.min(estimatedHeight, 260) && spaceAbove > spaceBelow

  placedAbove.value = placeAbove
  if (placeAbove) {
    const maxHeight = Math.max(120, Math.min(380, spaceAbove))
    const bottom = Math.max(margin, viewportHeight - rect.top + gap)
    dropdownStyle.value = {
      position: 'fixed',
      top: 'auto',
      bottom: `${Math.round(bottom)}px`,
      left: `${Math.round(left)}px`,
      width: `${Math.round(width)}px`,
      maxHeight: `${Math.round(maxHeight)}px`,
      zIndex: 'var(--z-popover, 600)',
    }
    return
  }

  const maxHeight = Math.max(120, Math.min(380, spaceBelow))
  const effectiveHeight = Math.min(estimatedHeight, maxHeight)
  const top = clamp(rect.bottom + gap, margin, Math.max(margin, viewportHeight - margin - effectiveHeight))
  dropdownStyle.value = {
    position: 'fixed',
    top: `${Math.round(top)}px`,
    bottom: 'auto',
    left: `${Math.round(left)}px`,
    width: `${Math.round(width)}px`,
    maxHeight: `${Math.round(maxHeight)}px`,
    zIndex: 'var(--z-popover, 600)',
  }
}

function resolveInitialHighlight(direction = 'down') {
  const options = selectableOptions.value
  if (!options.length) return -1
  const selectedIndex = options.findIndex((option) => isOptionSelected(option))
  if (selectedIndex >= 0) return selectedIndex
  return direction === 'up' ? options.length - 1 : 0
}

function scrollHighlightedIntoView() {
  if (highlightedIndex.value < 0 || !listRef.value) return
  const nodes = listRef.value.querySelectorAll('[role="option"]')
  nodes[highlightedIndex.value]?.scrollIntoView?.({ block: 'nearest' })
}

async function openDropdown(direction = 'down') {
  if (props.disabled) return
  open.value = true
  highlightedIndex.value = resolveInitialHighlight(direction)
  await nextTick()
  updateDropdownPosition()
  searchInputRef.value?.focus?.()
  scrollHighlightedIntoView()
  if (typeof requestAnimationFrame === 'function') {
    requestAnimationFrame(() => updateDropdownPosition())
  }
}

function close({ restoreFocus = false } = {}) {
  open.value = false
  searchQuery.value = ''
  statusFilter.value = ''
  sortBy.value = 'last_seen'
  highlightedIndex.value = -1
  placedAbove.value = false
  dropdownStyle.value = {}
  if (restoreFocus) {
    triggerRef.value?.focus?.()
  }
}

function toggleOpen() {
  if (props.disabled) return
  if (open.value) {
    close()
    return
  }
  openDropdown('down')
}

function selectOption(option, { restoreFocus = false } = {}) {
  if (!option) return
  if (option.kind === 'all') {
    emit('update:modelValue', ALL_AGENTS_FILTER)
    close({ restoreFocus })
    return
  }
  if (!option.id) return
  emit('update:modelValue', String(option.id))
  close({ restoreFocus })
}

function clearSearch() {
  searchQuery.value = ''
  nextTick(() => searchInputRef.value?.focus?.())
}

function moveHighlight(delta) {
  const total = selectableOptions.value.length
  if (!total) {
    highlightedIndex.value = -1
    return
  }
  if (highlightedIndex.value < 0) {
    highlightedIndex.value = delta > 0 ? 0 : total - 1
  } else {
    highlightedIndex.value = (highlightedIndex.value + delta + total) % total
  }
  nextTick(() => scrollHighlightedIntoView())
}

function onTriggerKeydown(event) {
  if (props.disabled) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    if (!open.value) openDropdown('down')
    return
  }
  if (event.key === 'ArrowUp') {
    event.preventDefault()
    if (!open.value) openDropdown('up')
    return
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    toggleOpen()
    return
  }
  if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    event.stopPropagation()
    close({ restoreFocus: true })
    return
  }
  if (event.key === 'Tab' && open.value) {
    close()
  }
}

function onDropdownKeydown(event) {
  if (event.key === 'Tab') {
    close()
    return
  }
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    moveHighlight(1)
    return
  }
  if (event.key === 'ArrowUp') {
    event.preventDefault()
    moveHighlight(-1)
    return
  }
  if (event.key === 'Home' && event.target !== searchInputRef.value && selectableOptions.value.length) {
    event.preventDefault()
    highlightedIndex.value = 0
    nextTick(() => scrollHighlightedIntoView())
    return
  }
  if (event.key === 'End' && event.target !== searchInputRef.value && selectableOptions.value.length) {
    event.preventDefault()
    highlightedIndex.value = selectableOptions.value.length - 1
    nextTick(() => scrollHighlightedIntoView())
    return
  }
  if (event.key === 'Enter') {
    const isSearchOrOption = (
      event.target === searchInputRef.value
      || event.target === dropdownRef.value
      || event.target?.getAttribute?.('role') === 'option'
    )
    if (isSearchOrOption && highlightedIndex.value >= 0 && selectableOptions.value[highlightedIndex.value]) {
      event.preventDefault()
      selectOption(selectableOptions.value[highlightedIndex.value], { restoreFocus: true })
    }
    return
  }
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    if (hasSearchQuery.value) {
      clearSearch()
      return
    }
    close({ restoreFocus: true })
  }
}

function handleClickOutside(event) {
  if (!open.value) return
  const target = event.target
  const inRoot = rootRef.value && rootRef.value.contains(target)
  const inDropdown = dropdownRef.value && dropdownRef.value.contains(target)
  if (!inRoot && !inDropdown) {
    close()
  }
}

function handleViewportChange(event) {
  if (!open.value) return
  if (event?.target && dropdownRef.value?.contains(event.target)) return
  updateDropdownPosition()
}

watch(() => props.disabled, (isDisabled) => {
  if (isDisabled && open.value) close()
})

watch(selectableOptions, (options) => {
  if (!open.value) return
  if (!options.length) {
    highlightedIndex.value = -1
    return
  }
  if (highlightedIndex.value < 0 || highlightedIndex.value >= options.length) {
    highlightedIndex.value = 0
  }
})

onMounted(() => {
  document.addEventListener('mousedown', handleClickOutside)
  window.addEventListener('resize', handleViewportChange)
  window.addEventListener('scroll', handleViewportChange, true)
})

onUnmounted(() => {
  document.removeEventListener('mousedown', handleClickOutside)
  window.removeEventListener('resize', handleViewportChange)
  window.removeEventListener('scroll', handleViewportChange, true)
})
</script>

<style scoped>
.agent-search-select {
  position: relative;
  min-width: 180px;
}

.agent-search-select--form {
  min-width: 0;
  width: 100%;
}

.agent-search-select__trigger {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 34px;
  padding: var(--space-1-5) var(--space-2-5);
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

.agent-search-select--form .agent-search-select__trigger {
  min-height: var(--control-height-md);
  padding: var(--space-2) var(--space-3);
  border-width: 1px;
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-xs);
}

.agent-search-select__trigger:hover:not(:disabled) {
  border-color: var(--color-primary);
  background: var(--color-bg-hover);
}

.agent-search-select__trigger:focus-visible:not(:disabled),
.agent-search-select__trigger--open:not(:disabled) {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.agent-search-select__trigger--invalid {
  border-color: var(--color-danger);
}

.agent-search-select__trigger--invalid:hover:not(:disabled),
.agent-search-select__trigger--invalid:focus-visible:not(:disabled),
.agent-search-select__trigger--invalid.agent-search-select__trigger--open:not(:disabled) {
  border-color: var(--color-danger);
  box-shadow: var(--shadow-focus-danger);
}

.agent-search-select__trigger:disabled,
.agent-search-select__trigger--disabled {
  opacity: 0.48;
  cursor: not-allowed;
  background: var(--color-bg-subtle);
}

.agent-search-select__label {
  flex: 1;
  min-width: 0;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-search-select__label--placeholder {
  color: var(--color-text-muted);
}

.agent-search-select__label--missing {
  color: var(--color-danger);
}

.agent-search-select__chevron {
  flex-shrink: 0;
  color: var(--color-text-muted);
  transition: transform var(--duration-fast) var(--ease-default);
}

.agent-search-select__chevron--open {
  transform: rotate(180deg);
}
</style>

<!-- Dropdown is teleported to body so modal scroll containers cannot clip it. -->
<style>
.agent-search-select__dropdown {
  position: fixed;
  z-index: var(--z-popover, 600);
  display: flex;
  flex-direction: column;
  width: min(320px, calc(100vw - 16px));
  background: var(--color-bg-surface-raised);
  border: var(--border-width-thick) solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  overflow: hidden;
  box-sizing: border-box;
}

.agent-search-select__search {
  flex-shrink: 0;
  padding: var(--space-2);
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
}

.agent-search-select__search-shell {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-height: 2rem;
  padding: 0 var(--space-1) 0 var(--space-2);
  border: var(--border-width-thick) solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.agent-search-select__search-shell:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.agent-search-select__search-icon {
  flex-shrink: 0;
  color: var(--color-text-muted);
}

.agent-search-select__search-input {
  width: 100%;
  min-width: 0;
  padding: var(--space-1) 0;
  border: none;
  background: transparent;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
}

.agent-search-select__search-input::-webkit-search-cancel-button {
  appearance: none;
}

.agent-search-select__search-clear {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 1.25rem;
  height: 1.25rem;
  margin: 0;
  padding: 0;
  border: none;
  border-radius: var(--radius-full);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.agent-search-select__search-clear:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.agent-search-select__search-clear:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
  color: var(--color-primary);
}

.agent-search-select__filters {
  flex-shrink: 0;
  display: flex;
  gap: var(--space-1);
  padding: var(--space-2);
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
  overflow-x: auto;
}

.agent-search-select__filter-btn {
  padding: var(--space-1) var(--space-2-5);
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
  white-space: nowrap;
  font-family: inherit;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.agent-search-select__filter-btn:hover {
  background: var(--color-bg-hover);
}

.agent-search-select__filter-btn--active {
  background: var(--color-primary);
  color: var(--color-text-inverse);
}

.agent-search-select__list {
  flex: 1 1 auto;
  min-height: 0;
  max-height: 240px;
  overflow-y: auto;
  padding: var(--space-1);
  scrollbar-width: thin;
}

.agent-search-select__option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2) var(--space-2-5);
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  font-family: inherit;
  text-align: left;
  transition: background var(--duration-fast) var(--ease-default);
}

.agent-search-select__option:hover,
.agent-search-select__option--highlighted {
  background: var(--color-bg-hover);
}

.agent-search-select__option--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.agent-search-select__option--active.agent-search-select__option--highlighted {
  background: color-mix(in srgb, var(--color-primary) 16%, transparent);
}

.agent-search-select__option-name {
  flex: 1;
  min-width: 0;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-search-select__option--active .agent-search-select__option-name {
  color: var(--color-primary);
  font-weight: var(--font-medium);
}

.agent-search-select__option-time {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.agent-search-select__empty {
  padding: var(--space-4);
  text-align: center;
  font-size: var(--text-sm);
  color: var(--color-text-muted);
}

.agent-search-select__sort {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  border-top: var(--border-width-thin) solid var(--color-border-subtle);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

.agent-search-select__sort-btn {
  padding: var(--space-0-5) var(--space-1-5);
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
  font-family: inherit;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.agent-search-select__sort-btn:hover {
  background: var(--color-bg-hover);
}

.agent-search-select__sort-btn--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: var(--font-medium);
}

.agent-search-select__status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.agent-search-select__status-dot--online {
  background: var(--color-success);
}

.agent-search-select__status-dot--offline {
  background: var(--color-text-muted);
}

.agent-search-select__status-dot--failed {
  background: var(--color-danger);
}

.agent-search-select__status-dot--pending {
  background: var(--color-warning);
}
</style>
