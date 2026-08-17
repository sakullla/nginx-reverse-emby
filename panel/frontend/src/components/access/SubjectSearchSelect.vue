<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, useAttrs, useId, watch } from 'vue'

defineOptions({ inheritAttrs: false })

const props = defineProps({
  modelValue: { type: String, default: '' },
  kind: { type: String, default: '' },
  users: { type: Array, default: () => [] },
  roles: { type: Array, default: () => [] },
  disabled: { type: Boolean, default: false },
  exclude: { type: Array, default: () => [] },
  placeholder: { type: String, default: '搜索用户名、显示名或角色名' }
})

const emit = defineEmits(['update:modelValue', 'update:kind', 'select'])

const attrs = useAttrs()
const instanceId = useId()
const open = ref(false)
const searchQuery = ref('')
const kindFilter = ref(normalizeKind(props.kind))
const highlightedIndex = ref(-1)
const rootRef = ref(null)
const searchInputRef = ref(null)

const rootClass = computed(() => attrs.class)
const rootStyle = computed(() => attrs.style)
const selectAttrs = computed(() => {
  const next = { ...attrs }
  delete next.class
  delete next.style
  return next
})

const userSubjects = computed(() => (props.users || []).map(toUserSubject).filter(Boolean))
const roleSubjects = computed(() => (props.roles || []).map(toRoleSubject).filter(Boolean))

const catalog = computed(() => {
  const kind = kindFilter.value
  if (kind === 'user') return userSubjects.value
  if (kind === 'role') return roleSubjects.value
  return [...userSubjects.value, ...roleSubjects.value]
})

const selectable = computed(() => catalog.value.filter((item) => !isExcluded(item)))

const displayedSubjects = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  if (!query) return selectable.value
  return selectable.value.filter((item) => item.searchText.includes(query))
})

const selectedSubject = computed(() => {
  const id = String(props.modelValue || '').trim()
  if (!id) return null
  const kind = normalizeKind(props.kind) || kindFilter.value
  const pool = kind ? (kind === 'role' ? roleSubjects.value : userSubjects.value) : [...userSubjects.value, ...roleSubjects.value]
  return pool.find((item) => item.id === id) || null
})

const selectedLabel = computed(() => {
  if (selectedSubject.value) return selectedSubject.value.label
  return props.modelValue ? props.modelValue : emptyLabel(kindFilter.value)
})

const selectedDetail = computed(() => selectedSubject.value?.detail || '')
const hasSearchQuery = computed(() => searchQuery.value.trim().length > 0)
const listboxId = computed(() => `${instanceId}-listbox`)
const emptyMessage = computed(() => {
  if (!selectable.value.length) return emptyLabel(kindFilter.value)
  if (kindFilter.value === 'role') return '没有匹配的角色'
  if (kindFilter.value === 'user') return '没有匹配的用户'
  return '没有匹配的用户或角色'
})

watch(() => props.kind, (value) => {
  kindFilter.value = normalizeKind(value)
})

watch([displayedSubjects, open], ([items, isOpen]) => {
  if (!isOpen) {
    highlightedIndex.value = -1
    return
  }
  const selected = items.findIndex((item) => isActive(item))
  highlightedIndex.value = selected >= 0 ? selected : (items.length ? 0 : -1)
})

watch(open, async (value) => {
  if (!value) return
  await nextTick()
  searchInputRef.value?.focus?.()
})

onMounted(() => document.addEventListener('mousedown', handleClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', handleClickOutside))

function normalizeKind(value) {
  const kind = String(value || '').trim().toLowerCase()
  return kind === 'user' || kind === 'role' ? kind : ''
}

function toUserSubject(user) {
  const id = String(user?.id || '').trim()
  if (!id) return null
  const username = String(user?.username || '').trim()
  const displayName = String(user?.display_name || '').trim()
  const label = displayName || username || id
  const detail = [
    username && username !== label ? username : '',
    user?.disabled ? '已停用' : ''
  ].filter(Boolean).join(' · ')
  return {
    kind: 'user',
    id,
    label,
    detail,
    searchText: [displayName, username].filter(Boolean).join(' ').toLowerCase()
  }
}

function toRoleSubject(role) {
  const id = String(role?.id || '').trim()
  if (!id) return null
  const name = String(role?.name || '').trim()
  const label = name || id
  return {
    kind: 'role',
    id,
    label,
    detail: '角色',
    searchText: name.toLowerCase()
  }
}

function isExcluded(subject) {
  return (props.exclude || []).some((item) => {
    if (!item) return false
    if (typeof item === 'string') {
      return item === `${subject.kind}:${subject.id}` || item === subject.id
    }
    return item.subject_kind === subject.kind && item.subject_id === subject.id
  })
}

function emptyLabel(kind) {
  if (kind === 'role') return '暂无角色可选项'
  if (kind === 'user') return '暂无用户可选项'
  return '暂无可选项'
}

function optionId(index) {
  return `${instanceId}-option-${index}`
}

function isActive(item) {
  return !!selectedSubject.value
    && selectedSubject.value.kind === item.kind
    && selectedSubject.value.id === item.id
}

function setKindFilter(next) {
  const kind = normalizeKind(next)
  kindFilter.value = kind
  searchQuery.value = ''
  if (kind && selectedSubject.value && selectedSubject.value.kind !== kind) {
    emit('update:modelValue', '')
  }
  if (kind) emit('update:kind', kind)
}

function selectSubject(subject) {
  if (!subject?.id || props.disabled) return
  emit('update:kind', subject.kind)
  emit('update:modelValue', subject.id)
  emit('select', {
    subject_kind: subject.kind,
    subject_id: subject.id,
    label: subject.label
  })
  close()
}

function selectById(rawId) {
  const id = String(rawId || '').trim()
  if (!id) {
    emit('update:modelValue', '')
    return
  }
  const kind = kindFilter.value
  const pool = kind ? selectable.value : [...userSubjects.value, ...roleSubjects.value].filter((item) => !isExcluded(item))
  const match = pool.find((item) => item.id === id)
  if (match) {
    selectSubject(match)
    return
  }
  emit('update:modelValue', id)
}

function onNativeChange(event) {
  selectById(event.target.value)
}

function clearSearch() {
  searchQuery.value = ''
  nextTick(() => searchInputRef.value?.focus?.())
}

function close() {
  open.value = false
  searchQuery.value = ''
  highlightedIndex.value = -1
}

function toggleOpen() {
  if (props.disabled) return
  if (open.value) {
    close()
    return
  }
  open.value = true
}

function handleClickOutside(event) {
  if (rootRef.value && !rootRef.value.contains(event.target)) close()
}

function moveHighlight(delta) {
  const count = displayedSubjects.value.length
  if (!count) {
    highlightedIndex.value = -1
    return
  }
  const current = highlightedIndex.value
  const next = current < 0 ? (delta > 0 ? 0 : count - 1) : (current + delta + count) % count
  highlightedIndex.value = next
}

function onTriggerKeydown(event) {
  if (props.disabled) return
  if (event.key === 'ArrowDown' || event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    if (!open.value) open.value = true
    else if (event.key === 'Enter' || event.key === ' ') selectHighlighted()
  } else if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    close()
  }
}

function onSearchKeydown(event) {
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
  if (event.key === 'Enter') {
    event.preventDefault()
    selectHighlighted()
    return
  }
  if (event.key === 'Escape') {
    event.preventDefault()
    if (hasSearchQuery.value) {
      clearSearch()
      return
    }
    close()
  }
}

function selectHighlighted() {
  const item = displayedSubjects.value[highlightedIndex.value]
  if (item) selectSubject(item)
}

const kindOptions = [
  { value: '', label: '全部' },
  { value: 'user', label: '用户' },
  { value: 'role', label: '角色' }
]
</script>

<template>
  <div
    ref="rootRef"
    class="subject-search-select"
    :class="[rootClass, { 'subject-search-select--open': open, 'subject-search-select--disabled': disabled }]"
    :style="rootStyle"
  >
    <select
      class="subject-search-select__native"
      :value="modelValue"
      :disabled="disabled"
      tabindex="-1"
      aria-hidden="true"
      v-bind="selectAttrs"
      @change="onNativeChange"
    >
      <option value="">{{ emptyLabel(kindFilter) }}</option>
      <option
        v-for="item in selectable"
        :key="`${item.kind}:${item.id}`"
        :value="item.id"
      >
        {{ item.label }}
      </option>
    </select>

    <button
      type="button"
      class="subject-search-select__trigger"
      :disabled="disabled"
      :aria-expanded="open ? 'true' : 'false'"
      aria-haspopup="listbox"
      :aria-controls="listboxId"
      :aria-label="selectedSubject ? selectedLabel : '选择要授权的用户或角色'"
      data-test="subject-search-trigger"
      @click="toggleOpen"
      @keydown="onTriggerKeydown"
    >
      <span class="subject-search-select__value">
        <span class="subject-search-select__label">{{ selectedLabel }}</span>
        <small v-if="selectedDetail" class="subject-search-select__detail">{{ selectedDetail }}</small>
      </span>
      <svg class="subject-search-select__chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <polyline points="6 9 12 15 18 9" />
      </svg>
    </button>

    <div v-if="open" class="subject-search-select__dropdown">
      <div class="subject-search-select__search">
        <div class="subject-search-select__search-shell">
          <svg class="subject-search-select__search-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="M20 20l-3.5-3.5" />
          </svg>
          <input
            ref="searchInputRef"
            v-model="searchQuery"
            type="search"
            class="subject-search-select__search-input"
            data-test="subject-search-query"
            :placeholder="placeholder"
            aria-label="搜索用户名、显示名或角色名"
            :aria-controls="listboxId"
            :aria-activedescendant="highlightedIndex >= 0 ? optionId(highlightedIndex) : undefined"
            autocomplete="off"
            @keydown="onSearchKeydown"
          >
          <button
            v-if="hasSearchQuery"
            type="button"
            class="subject-search-select__search-clear"
            aria-label="清空搜索"
            @click="clearSearch"
          >
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" aria-hidden="true">
              <path d="M18 6L6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>

      <div class="subject-search-select__filters" role="tablist" aria-label="主体类型">
        <button
          v-for="option in kindOptions"
          :key="option.value || 'all'"
          type="button"
          class="subject-search-select__filter-btn"
          :class="{ 'subject-search-select__filter-btn--active': kindFilter === option.value }"
          :data-test="`subject-kind-${option.value || 'all'}`"
          @click="setKindFilter(option.value)"
        >
          {{ option.label }}
        </button>
      </div>

      <div
        :id="listboxId"
        class="subject-search-select__list"
        role="listbox"
        aria-label="可授权主体"
      >
        <button
          v-for="(item, index) in displayedSubjects"
          :id="optionId(index)"
          :key="`${item.kind}:${item.id}`"
          type="button"
          class="subject-search-select__option"
          :class="{
            'subject-search-select__option--active': isActive(item),
            'subject-search-select__option--highlighted': index === highlightedIndex
          }"
          role="option"
          :aria-selected="isActive(item) ? 'true' : 'false'"
          :data-test="`subject-option-${item.kind}-${item.id}`"
          @click="selectSubject(item)"
          @mouseenter="highlightedIndex = index"
        >
          <span class="subject-search-select__option-kind">{{ item.kind === 'role' ? '角色' : '用户' }}</span>
          <span class="subject-search-select__option-name">{{ item.label }}</span>
          <span v-if="item.detail" class="subject-search-select__option-detail">{{ item.detail }}</span>
        </button>
        <div v-if="!displayedSubjects.length" class="subject-search-select__empty" data-test="subject-search-empty">
          {{ emptyMessage }}
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.subject-search-select {
  position: relative;
  min-width: 12rem;
}

.subject-search-select--disabled {
  opacity: 0.65;
}

.subject-search-select__native {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.subject-search-select__trigger {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  min-height: 2.5rem;
  padding: 0.65rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.subject-search-select__trigger:hover:not(:disabled) {
  border-color: var(--color-primary);
}

.subject-search-select__trigger:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.subject-search-select__trigger:disabled {
  cursor: not-allowed;
}

.subject-search-select__value {
  display: grid;
  flex: 1;
  min-width: 0;
  gap: 0.1rem;
}

.subject-search-select__label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.subject-search-select__detail,
.subject-search-select__option-detail {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.subject-search-select__chevron {
  flex-shrink: 0;
  color: var(--color-text-muted);
}

.subject-search-select__dropdown {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: var(--z-dropdown);
  width: min(22rem, 80vw);
  overflow: hidden;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface-raised, var(--color-bg-surface));
  box-shadow: var(--shadow-xl);
}

.subject-search-select__search {
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.subject-search-select__search-shell {
  display: flex;
  align-items: center;
  gap: 0.3rem;
  min-height: 2rem;
  padding: 0 0.3rem 0 0.55rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle, var(--color-bg-canvas));
}

.subject-search-select__search-shell:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.subject-search-select__search-icon {
  flex-shrink: 0;
  color: var(--color-text-muted);
}

.subject-search-select__search-input {
  width: 100%;
  min-width: 0;
  padding: 0.3rem 0;
  border: none;
  background: transparent;
  color: var(--color-text-primary);
  font: inherit;
  font-size: 0.8125rem;
  outline: none;
}

.subject-search-select__search-input::-webkit-search-cancel-button {
  appearance: none;
}

.subject-search-select__search-clear {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.25rem;
  height: 1.25rem;
  padding: 0;
  border: none;
  border-radius: 999px;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}

.subject-search-select__search-clear:hover,
.subject-search-select__search-clear:focus-visible {
  color: var(--color-text-primary);
}

.subject-search-select__filters {
  display: flex;
  gap: 0.25rem;
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.subject-search-select__filter-btn {
  padding: 0.25rem 0.625rem;
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle, var(--color-bg-canvas));
  color: var(--color-text-secondary);
  font: inherit;
  font-size: 0.75rem;
  cursor: pointer;
}

.subject-search-select__filter-btn--active {
  background: var(--color-primary);
  color: #fff;
}

.subject-search-select__list {
  max-height: 15rem;
  padding: 0.25rem;
  overflow-y: auto;
}

.subject-search-select__option {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.5rem 0.65rem;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.subject-search-select__option--highlighted,
.subject-search-select__option:hover {
  background: var(--color-bg-hover);
}

.subject-search-select__option--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.subject-search-select__option-kind {
  flex-shrink: 0;
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.subject-search-select__option-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.8125rem;
}

.subject-search-select__empty {
  padding: 1rem;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  text-align: center;
}
</style>
