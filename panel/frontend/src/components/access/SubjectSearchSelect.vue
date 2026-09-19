<script setup>
import { computed, nextTick, onMounted, ref, useAttrs, useId, watch } from 'vue'

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

const emit = defineEmits(['update:modelValue', 'update:kind', 'update:selected', 'select'])
const DISPLAY_LIMIT = 250
const TOKEN_LIMIT = 6

const attrs = useAttrs()
const instanceId = useId()
const searchQuery = ref('')
const kindFilter = ref(normalizeKind(props.kind) || 'user')
const highlightedIndex = ref(-1)
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

const matchedSubjects = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  if (!query) return selectable.value
  return selectable.value.filter((item) => item.searchText.includes(query))
})

const displayedSubjects = computed(() => matchedSubjects.value.slice(0, DISPLAY_LIMIT))
const hiddenSubjectCount = computed(() => Math.max(0, matchedSubjects.value.length - displayedSubjects.value.length))

const allSubjects = computed(() => [...userSubjects.value, ...roleSubjects.value])
const selectedKeys = ref([])
const selectedSubjects = computed(() => (
  selectedKeys.value
    .map((key) => allSubjects.value.find((item) => subjectKey(item) === key))
    .filter(Boolean)
))
const selectedCount = computed(() => selectedSubjects.value.length)
const visibleTokens = computed(() => selectedSubjects.value.slice(0, TOKEN_LIMIT))
const hiddenTokenCount = computed(() => Math.max(0, selectedCount.value - visibleTokens.value.length))
const allDisplayedSelected = computed(() => (
  displayedSubjects.value.length > 0
  && displayedSubjects.value.every((item) => selectedKeys.value.includes(subjectKey(item)))
))

const hasSearchQuery = computed(() => searchQuery.value.trim().length > 0)
const listboxId = computed(() => `${instanceId}-listbox`)
const emptyMessage = computed(() => {
  const query = searchQuery.value.trim()
  if (query) {
    if (kindFilter.value === 'role') return `没有叫「${query}」的角色。`
    if (kindFilter.value === 'user') return `没有叫「${query}」的用户。`
    return `没有叫「${query}」的用户或角色。`
  }
  if (kindFilter.value === 'user' && !userSubjects.value.length) return '还没有可授权的用户。'
  if (kindFilter.value === 'role' && !roleSubjects.value.length) return '还没有可授权的角色。'
  if (!selectable.value.length && kindFilter.value === 'user') return '这个组里的用户都已授权。改选「角色」可以继续添加。'
  if (!selectable.value.length && kindFilter.value === 'role') return '这个组里的角色都已授权。改选「用户」可以继续添加。'
  if (!selectable.value.length) return emptyLabel(kindFilter.value)
  if (kindFilter.value === 'role') return '没有匹配的角色。'
  if (kindFilter.value === 'user') return '没有匹配的用户。'
  return '没有匹配的用户或角色。'
})

watch(() => props.kind, (value) => {
  const kind = normalizeKind(value)
  if (kind) kindFilter.value = kind
})

watch([displayedSubjects], ([items]) => {
  const selected = items.findIndex((item) => isChecked(item))
  highlightedIndex.value = selected >= 0 ? selected : (items.length ? 0 : -1)
})

watch(() => [props.users, props.roles, props.exclude], pruneExcluded, { deep: true })

onMounted(() => emitSelected(false))

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
    searchText: [displayName, username, id].filter(Boolean).join(' ').toLowerCase()
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
    detail: id && id !== label ? id : '',
    searchText: [name, id].filter(Boolean).join(' ').toLowerCase()
  }
}

function subjectKey(subject) {
  return `${subject.kind}:${subject.id}`
}

function isExcluded(subject) {
  return (props.exclude || []).some((item) => {
    if (!item) return false
    if (typeof item === 'string') {
      return item === subjectKey(subject) || item === subject.id
    }
    return item.subject_kind === subject.kind && item.subject_id === subject.id
  })
}

function pruneExcluded() {
  if (!selectedKeys.value.length) return
  if (!allSubjects.value.length) return
  const next = selectedKeys.value.filter((key) => {
    const match = allSubjects.value.find((item) => subjectKey(item) === key)
    return match && !isExcluded(match)
  })
  if (next.length === selectedKeys.value.length && next.every((key, index) => key === selectedKeys.value[index])) return
  selectedKeys.value = next
  emitSelected(false)
}

function emptyLabel(kind) {
  if (kind === 'role') return '暂无角色可选项'
  if (kind === 'user') return '暂无用户可选项'
  return '暂无可选项'
}

function optionId(index) {
  return `${instanceId}-option-${index}`
}

function isChecked(item) {
  return selectedKeys.value.includes(subjectKey(item))
}

function setKindFilter(next) {
  const kind = normalizeKind(next) || 'user'
  kindFilter.value = kind
  searchQuery.value = ''
  emit('update:kind', kind)
}

function selectedPayload(list = selectedSubjects.value) {
  return list.map((item) => ({
    subject_kind: item.kind,
    subject_id: item.id,
    label: item.label
  }))
}

function emitSelected(announce = true) {
  const items = selectedPayload()
  const last = items.at(-1) || null
  emit('update:selected', items)
  emit('update:modelValue', last?.subject_id || '')
  if (last) emit('update:kind', last.subject_kind)
  if (announce) emit('select', last)
}

function toggleSubject(subject) {
  if (!subject?.id || props.disabled) return
  const key = subjectKey(subject)
  selectedKeys.value = isChecked(subject)
    ? selectedKeys.value.filter((item) => item !== key)
    : [...selectedKeys.value, key]
  emitSelected()
}

function toggleAllDisplayed() {
  if (props.disabled || !displayedSubjects.value.length) return
  if (allDisplayedSelected.value) {
    const drop = new Set(displayedSubjects.value.map(subjectKey))
    selectedKeys.value = selectedKeys.value.filter((key) => !drop.has(key))
  } else {
    const next = new Set(selectedKeys.value)
    displayedSubjects.value.forEach((item) => next.add(subjectKey(item)))
    selectedKeys.value = [...next]
  }
  emitSelected()
}

function clearSelection() {
  if (props.disabled || !selectedKeys.value.length) return
  selectedKeys.value = []
  emitSelected()
}

function selectById(rawId) {
  const id = String(rawId || '').trim()
  if (!id) {
    selectedKeys.value = []
    emitSelected()
    return
  }
  const match = selectable.value.find((item) => item.id === id)
  if (match) {
    if (!isChecked(match)) toggleSubject(match)
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
    const item = displayedSubjects.value[highlightedIndex.value]
    if (item) toggleSubject(item)
    return
  }
  if (event.key === 'Backspace' && !searchQuery.value && selectedSubjects.value.length) {
    event.preventDefault()
    toggleSubject(selectedSubjects.value.at(-1))
    return
  }
  if (event.key === 'Escape' && hasSearchQuery.value) {
    event.preventDefault()
    clearSearch()
  }
}

const kindOptions = [
  { value: 'user', label: '用户' },
  { value: 'role', label: '角色' }
]
</script>

<template>
  <div
    class="subject-search-select"
    :class="[rootClass, { 'subject-search-select--disabled': disabled }]"
    :style="rootStyle"
    data-test="subject-search"
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

    <div class="subject-search-select__filters" role="tablist" aria-label="主体类型">
      <button
        v-for="option in kindOptions"
        :key="option.value"
        type="button"
        class="subject-search-select__filter"
        :class="{ 'subject-search-select__filter--on': kindFilter === option.value }"
        :data-test="`subject-kind-${option.value}`"
        :disabled="disabled"
        @click="setKindFilter(option.value)"
      >
        {{ option.label }}
      </button>
    </div>

    <div class="subject-search-select__box">
      <div v-if="selectedCount" class="subject-search-select__tokens">
        <button
          v-for="item in visibleTokens"
          :key="subjectKey(item)"
          type="button"
          class="subject-search-select__token"
          :disabled="disabled"
          :aria-label="`取消选择 ${item.label}`"
          @click="toggleSubject(item)"
        >
          <span>{{ item.label }}</span>
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
        <span v-if="hiddenTokenCount" class="subject-search-select__token-more">+{{ hiddenTokenCount }}</span>
        <button
          type="button"
          class="subject-search-select__clear-tokens"
          :disabled="disabled"
          @click="clearSelection"
        >
          清除
        </button>
      </div>

      <div class="subject-search-select__search">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-3.5-3.5" />
        </svg>
        <input
          ref="searchInputRef"
          v-model="searchQuery"
          type="search"
          data-test="subject-search-query"
          :placeholder="kindFilter === 'role' ? '搜索角色名' : '搜索用户名或显示名'"
          :disabled="disabled"
          :aria-label="placeholder"
          :aria-controls="listboxId"
          :aria-expanded="displayedSubjects.length ? 'true' : 'false'"
          :aria-activedescendant="highlightedIndex >= 0 ? optionId(highlightedIndex) : undefined"
          autocomplete="off"
          @keydown="onSearchKeydown"
        >
        <button
          v-if="hasSearchQuery"
          type="button"
          class="subject-search-select__clear-query"
          aria-label="清空搜索"
          @click="clearSearch"
        >
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" aria-hidden="true">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>

      <p v-if="!displayedSubjects.length" class="subject-search-select__empty" data-test="subject-search-empty">
        {{ emptyMessage }}
      </p>
      <template v-else>
        <div class="subject-search-select__toolbar">
          <button
            type="button"
            class="subject-search-select__select-all"
            data-test="subject-select-all"
            :disabled="disabled || !displayedSubjects.length"
            @click="toggleAllDisplayed"
          >
            {{ allDisplayedSelected ? '取消全选' : '全选' }}
          </button>
          <p class="subject-search-select__count">
            {{ matchedSubjects.length }} 人
            <template v-if="selectedCount"> · 已选 {{ selectedCount }}</template>
            <template v-if="hiddenSubjectCount"> · 显示前 {{ displayedSubjects.length }}</template>
          </p>
        </div>
        <div
          :id="listboxId"
          class="subject-search-select__list"
          role="listbox"
          aria-multiselectable="true"
          aria-label="可授权主体"
        >
          <button
            v-for="(item, index) in displayedSubjects"
            :id="optionId(index)"
            :key="`${item.kind}:${item.id}`"
            type="button"
            class="subject-search-select__option"
            :class="{
              'subject-search-select__option--active': isChecked(item),
              'subject-search-select__option--highlighted': index === highlightedIndex
            }"
            role="option"
            :aria-selected="isChecked(item) ? 'true' : 'false'"
            :disabled="disabled"
            :data-test="`subject-option-${item.kind}-${item.id}`"
            @mouseenter="highlightedIndex = index"
            @click="toggleSubject(item)"
          >
            <span class="subject-search-select__check" :class="{ 'subject-search-select__check--on': isChecked(item) }" aria-hidden="true"></span>
            <span class="subject-search-select__identity">
              <span class="subject-search-select__name">{{ item.label }}</span>
              <span v-if="item.detail" class="subject-search-select__detail">{{ item.detail }}</span>
            </span>
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.subject-search-select {
  display: grid;
  gap: 0.45rem;
  min-width: 0;
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

.subject-search-select__filters {
  display: inline-flex;
  width: max-content;
  padding: 0.14rem;
  border-radius: 999px;
  background: var(--color-bg-subtle);
}

.subject-search-select__filter {
  min-width: 3.25rem;
  padding: 0.28rem 0.8rem;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-size: 0.8125rem;
  line-height: 1.2;
  cursor: pointer;
}

.subject-search-select__filter--on {
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  box-shadow: 0 0 0 1px var(--color-border-subtle);
  font-weight: 650;
}

.subject-search-select__box {
  display: grid;
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}

.subject-search-select__tokens {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem;
  padding: 0.55rem 0.65rem 0;
}

.subject-search-select__token {
  display: inline-flex;
  align-items: center;
  gap: 0.28rem;
  max-width: 11rem;
  padding: 0.18rem 0.4rem 0.18rem 0.5rem;
  border: 0;
  border-radius: 999px;
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  font: inherit;
  font-size: 0.75rem;
  line-height: 1.2;
  cursor: pointer;
}

.subject-search-select__token span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.subject-search-select__token:hover:not(:disabled) {
  background: var(--color-bg-hover);
}

.subject-search-select__token-more,
.subject-search-select__clear-tokens {
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.subject-search-select__clear-tokens {
  padding: 0;
  border: 0;
  background: transparent;
  font: inherit;
  cursor: pointer;
}

.subject-search-select__clear-tokens:hover:not(:disabled) {
  color: var(--color-text-primary);
}

.subject-search-select__search {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  min-width: 0;
  padding: 0.15rem 0.7rem;
}

.subject-search-select__search svg {
  flex-shrink: 0;
  color: var(--color-text-muted);
}

.subject-search-select__search input {
  flex: 1;
  min-width: 0;
  width: 100%;
  padding: 0.55rem 0;
  border: 0;
  background: transparent;
  color: var(--color-text-primary);
  font: inherit;
  font-size: 0.875rem;
  outline: none;
}

.subject-search-select__search input::-webkit-search-decoration,
.subject-search-select__search input::-webkit-search-cancel-button {
  appearance: none;
}

.subject-search-select__clear-query {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.4rem;
  height: 1.4rem;
  padding: 0;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}

.subject-search-select__empty {
  margin: 0;
  padding: 0 0.85rem 0.8rem;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  line-height: 1.45;
}

.subject-search-select__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  padding: 0.15rem 0.75rem 0.4rem;
  border-top: 1px solid var(--color-border-subtle);
}

.subject-search-select__select-all {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--color-primary);
  font: inherit;
  font-size: 0.75rem;
  cursor: pointer;
}

.subject-search-select__select-all:disabled {
  color: var(--color-text-muted);
  cursor: not-allowed;
}

.subject-search-select__count {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.subject-search-select__list {
  max-height: 11.5rem;
  overflow-y: auto;
  border-top: 1px solid var(--color-border-subtle);
}

.subject-search-select__option {
  display: grid;
  grid-template-columns: 1rem minmax(0, 1fr);
  align-items: center;
  column-gap: 0.65rem;
  width: 100%;
  padding: 0.5rem 0.75rem;
  border: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.subject-search-select__option:last-child {
  border-bottom: 0;
}

.subject-search-select__option--highlighted,
.subject-search-select__option:hover {
  background: var(--color-bg-hover);
}

.subject-search-select__option--active {
  background: var(--color-primary-subtle);
}

.subject-search-select__check {
  width: 1rem;
  height: 1rem;
  flex-shrink: 0;
  border: 1px solid var(--color-border-strong);
  border-radius: 0.25rem;
  background: var(--color-bg-canvas);
}

.subject-search-select__check--on {
  border-color: var(--color-primary);
  background: var(--color-primary);
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='white' stroke-width='2.4' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M3.5 8.5l3 3 6-6'/%3E%3C/svg%3E");
  background-size: 0.75rem;
  background-position: center;
  background-repeat: no-repeat;
}

.subject-search-select__identity {
  display: grid;
  min-width: 0;
  gap: 0.05rem;
}

.subject-search-select__name,
.subject-search-select__detail {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.subject-search-select__name {
  font-size: 0.875rem;
  font-weight: 600;
}

.subject-search-select__detail {
  color: var(--color-text-muted);
  font-size: 0.75rem;
}
</style>
