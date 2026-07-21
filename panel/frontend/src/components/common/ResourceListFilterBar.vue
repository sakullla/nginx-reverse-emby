<template>
  <div class="resource-list-filter-bar">
    <div class="resource-list-filter-bar__toolbar">
      <div class="resource-list-filter-bar__field resource-list-filter-bar__field--agent">
        <label v-if="showLabels" class="resource-list-filter-bar__label">节点</label>
        <AgentSearchSelect
          :model-value="agentId"
          :agents="agents"
          @update:model-value="onAgentUpdate"
        />
      </div>

      <div v-if="showSearch" class="resource-list-filter-bar__field resource-list-filter-bar__field--search">
        <label v-if="showLabels" class="resource-list-filter-bar__label">搜索</label>
        <div class="resource-list-filter-bar__search-shell">
          <svg
            class="resource-list-filter-bar__search-icon"
            width="14"
            height="14"
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
            class="resource-list-filter-bar__input"
            type="search"
            :value="q"
            :placeholder="searchPlaceholder"
            aria-label="搜索资源"
            @input="onSearchInput"
            @keydown.esc.prevent="clearSearch"
          />
          <button
            v-if="hasSearchQuery"
            type="button"
            class="resource-list-filter-bar__clear"
            aria-label="清空搜索"
            title="清空搜索"
            @click="clearSearch"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" aria-hidden="true">
              <path d="M18 6L6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>

      <div
        v-if="hasPanelFields"
        class="resource-list-filter-bar__field resource-list-filter-bar__field--filter"
        ref="filterRootRef"
      >
        <button
          type="button"
          class="resource-list-filter-bar__filter-trigger"
          :class="{ 'resource-list-filter-bar__filter-trigger--active': activeFilterCount > 0 || panelOpen }"
          :aria-expanded="panelOpen ? 'true' : 'false'"
          aria-haspopup="dialog"
          @click="togglePanel"
        >
          <svg
            class="resource-list-filter-bar__filter-icon"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            aria-hidden="true"
          >
            <path d="M4 5h16l-6 7v5l-4 2v-7L4 5z" />
          </svg>
          <span>筛选</span>
          <span
            v-if="activeFilterCount > 0"
            class="resource-list-filter-bar__filter-badge"
          >{{ activeFilterCount }}</span>
        </button>

        <div
          v-if="panelOpen"
          class="resource-list-filter-bar__panel"
          role="dialog"
          aria-label="筛选条件"
        >
          <div class="resource-list-filter-bar__panel-header">
            <span class="resource-list-filter-bar__panel-title">筛选条件</span>
            <button
              type="button"
              class="resource-list-filter-bar__reset"
              :disabled="activeFilterCount === 0"
              @click="resetFilters"
            >
              重置
            </button>
          </div>

          <div
            v-for="field in panelFields"
            :key="field.key"
            class="resource-list-filter-bar__panel-field"
          >
            <label class="resource-list-filter-bar__label">{{ field.label || field.key }}</label>
            <select
              v-if="field.type === 'select'"
              class="resource-list-filter-bar__select"
              :value="resolvedValue(field)"
              :aria-label="field.label || field.key"
              @change="onSelectChange(field, $event.target.value)"
            >
              <option
                v-for="option in field.options || []"
                :key="`${field.key}:${option.value}`"
                :value="option.value"
              >
                {{ option.label }}
              </option>
            </select>
            <div
              v-else-if="field.type === 'multi'"
              class="resource-list-filter-bar__multi"
              role="group"
              :aria-label="field.label || field.key"
            >
              <button
                v-for="option in field.options || []"
                :key="`${field.key}:${option.value}`"
                type="button"
                class="resource-list-filter-bar__chip resource-list-filter-bar__chip--panel"
                :class="{ 'resource-list-filter-bar__chip--active': isMultiSelected(field, option.value) }"
                :aria-pressed="isMultiSelected(field, option.value) ? 'true' : 'false'"
                @click="onMultiToggle(field, option.value)"
              >
                {{ option.label }}
              </button>
              <span v-if="!(field.options || []).length" class="resource-list-filter-bar__multi-empty">暂无可选项</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-if="chipFields.length" class="resource-list-filter-bar__chips" role="group" aria-label="快捷筛选">
      <template v-for="field in chipFields" :key="field.key">
        <button
          v-for="option in chipOptions(field)"
          :key="`${field.key}:${option.value}`"
          type="button"
          class="resource-list-filter-bar__chip"
          :class="{ 'resource-list-filter-bar__chip--active': resolvedValue(field) === String(option.value) }"
          :aria-pressed="resolvedValue(field) === String(option.value) ? 'true' : 'false'"
          @click="onChipToggle(field, option.value)"
        >
          {{ option.label }}
        </button>
      </template>
    </div>

    <div v-if="conditionTags.length" class="resource-list-filter-bar__conditions" aria-label="已选筛选条件">
      <button
        v-for="tag in conditionTags"
        :key="tag.key"
        type="button"
        class="resource-list-filter-bar__condition"
        :title="`移除筛选：${tag.label}`"
        @click="removeCondition(tag)"
      >
        <span class="resource-list-filter-bar__condition-label">{{ tag.label }}: {{ tag.text }}</span>
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
          <path d="M18 6L6 18M6 6l12 12" />
        </svg>
      </button>
      <button
        type="button"
        class="resource-list-filter-bar__reset resource-list-filter-bar__reset--inline"
        @click="resetFilters"
      >
        重置全部
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AgentSearchSelect from './AgentSearchSelect.vue'

const props = defineProps({
  agentId: { type: String, default: '' },
  agents: { type: Array, default: () => [] },
  /** agentId 等于该值时视为「全部节点」基线，不显示节点条件标签 */
  agentBaseline: { type: String, default: '' },
  q: { type: String, default: '' },
  showSearch: { type: Boolean, default: true },
  searchPlaceholder: { type: String, default: '搜索...' },
  showLabels: { type: Boolean, default: false },
  /**
   * Declarative filter fields, e.g.
   * [{ key: 'enabled', label: '启用状态', type: 'chip'|'select'|'multi',
   *    defaultValue: '', options: [{ value: '', label: '全部' }, ...] }]
   * - chip: non-baseline options render as quick-toggle chips
   * - select: single-select inside the filter panel
   * - multi: multi-select toggle chips inside the filter panel (value is an array)
   */
  filterFields: { type: Array, default: () => [] },
  /** Map of field.key -> current value (string for chip/select, array for multi) */
  filterValues: { type: Object, default: () => ({}) }
})

const emit = defineEmits([
  'update:agentId',
  'update:q',
  'update:filter',
  'change'
])

const panelOpen = ref(false)
const filterRootRef = ref(null)

const hasSearchQuery = computed(() => String(props.q || '').length > 0)

function baselineOf(field) {
  if (field.type === 'multi') return []
  return field.defaultValue ?? ''
}

function resolvedValue(field) {
  const current = props.filterValues?.[field.key]
  if (current === undefined || current === null) return baselineOf(field)
  if (field.type === 'multi') return Array.isArray(current) ? current : []
  return String(current)
}

function isActive(field) {
  const value = resolvedValue(field)
  if (field.type === 'multi') return value.length > 0
  return value !== String(baselineOf(field))
}

const chipFields = computed(() =>
  (props.filterFields || []).filter((field) => field.type === 'chip')
)

const panelFields = computed(() =>
  (props.filterFields || []).filter((field) => field.type === 'select' || field.type === 'multi')
)

const hasPanelFields = computed(() => panelFields.value.length > 0)

function chipOptions(field) {
  const baseline = String(baselineOf(field))
  return (field.options || []).filter((option) => String(option.value) !== baseline)
}

const activeFilterCount = computed(() =>
  (props.filterFields || []).reduce((count, field) => (isActive(field) ? count + 1 : count), 0)
)

function optionLabel(field, value) {
  const hit = (field.options || []).find((option) => String(option.value) === String(value))
  return hit ? hit.label : String(value)
}

const conditionTags = computed(() => {
  const tags = []
  if (props.agentId && String(props.agentId) !== String(props.agentBaseline)) {
    const agent = (props.agents || []).find((item) => String(item?.id) === String(props.agentId))
    tags.push({ kind: 'agent', key: '__agent__', label: '节点', text: agent?.name || String(props.agentId) })
  }
  for (const field of props.filterFields || []) {
    if (!isActive(field)) continue
    const value = resolvedValue(field)
    const text = field.type === 'multi'
      ? value.map((item) => optionLabel(field, item)).join('、')
      : optionLabel(field, value)
    tags.push({ kind: 'field', key: field.key, field, label: field.label || field.key, text })
  }
  return tags
})

function emitFilter(key, value) {
  emit('update:filter', { key, value })
  emit('change', { type: 'filter', key, value })
}

function onAgentUpdate(value) {
  emit('update:agentId', value)
  emit('change', { type: 'agentId', value })
}

function onSearchInput(event) {
  const value = event?.target?.value ?? ''
  emit('update:q', value)
  emit('change', { type: 'q', value })
}

function clearSearch() {
  if (!hasSearchQuery.value) return
  emit('update:q', '')
  emit('change', { type: 'q', value: '' })
}

function onChipToggle(field, optionValue) {
  const next = resolvedValue(field) === String(optionValue) ? baselineOf(field) : String(optionValue)
  emitFilter(field.key, next)
}

function onSelectChange(field, value) {
  emitFilter(field.key, String(value))
}

function isMultiSelected(field, optionValue) {
  return resolvedValue(field).some((item) => String(item) === String(optionValue))
}

function onMultiToggle(field, optionValue) {
  const current = resolvedValue(field)
  const exists = current.some((item) => String(item) === String(optionValue))
  const next = exists
    ? current.filter((item) => String(item) !== String(optionValue))
    : [...current, String(optionValue)]
  emitFilter(field.key, next)
}

function removeCondition(tag) {
  if (tag.kind === 'agent') {
    emit('update:agentId', props.agentBaseline)
    emit('change', { type: 'agentId', value: props.agentBaseline })
    return
  }
  emitFilter(tag.field.key, baselineOf(tag.field))
}

function resetFilters() {
  for (const field of props.filterFields || []) {
    if (isActive(field)) emitFilter(field.key, baselineOf(field))
  }
}

function togglePanel() {
  panelOpen.value = !panelOpen.value
}

function closePanel() {
  panelOpen.value = false
}

function handleClickOutside(event) {
  if (!panelOpen.value) return
  if (filterRootRef.value && !filterRootRef.value.contains(event.target)) {
    closePanel()
  }
}

function handleEscape(event) {
  if (event.key === 'Escape' && panelOpen.value) {
    closePanel()
  }
}

onMounted(() => {
  document.addEventListener('mousedown', handleClickOutside)
  document.addEventListener('keydown', handleEscape)
})

onUnmounted(() => {
  document.removeEventListener('mousedown', handleClickOutside)
  document.removeEventListener('keydown', handleEscape)
})
</script>

<style scoped>
.resource-list-filter-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem 0.65rem;
  margin-bottom: 0.875rem;
}

.resource-list-filter-bar__toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.45rem;
  flex: 1 1 100%;
  min-width: 0;
}

.resource-list-filter-bar__field {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  min-width: 0;
}

.resource-list-filter-bar__field--agent {
  width: 11.5rem;
  flex: 0 0 11.5rem;
}

.resource-list-filter-bar__field--agent :deep(.agent-search-select) {
  min-width: 0;
  width: 100%;
}

.resource-list-filter-bar__field--search {
  flex: 0 1 16rem;
  max-width: 18rem;
  min-width: 11rem;
}

.resource-list-filter-bar__field--filter {
  position: relative;
  flex: 0 0 auto;
}

.resource-list-filter-bar__label {
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.resource-list-filter-bar__search-shell {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  min-height: 34px;
  padding: 0 0.45rem 0 0.7rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.resource-list-filter-bar__search-shell:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.resource-list-filter-bar__search-icon {
  flex-shrink: 0;
  color: var(--color-text-muted);
}

.resource-list-filter-bar__input {
  width: 100%;
  min-width: 0;
  border: none;
  background: transparent;
  color: var(--color-text-primary);
  font-size: 0.875rem;
  font-family: inherit;
  outline: none;
  padding: 0.35rem 0;
}

.resource-list-filter-bar__input::-webkit-search-cancel-button {
  appearance: none;
}

.resource-list-filter-bar__clear {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 1.35rem;
  height: 1.35rem;
  margin: 0;
  padding: 0;
  border: none;
  border-radius: 999px;
  background: var(--color-bg-subtle);
  color: var(--color-text-muted);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.resource-list-filter-bar__clear:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.resource-list-filter-bar__clear:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
  color: var(--color-primary);
}

.resource-list-filter-bar__filter-trigger {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  min-height: 34px;
  padding: 0.35rem 0.7rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  font-family: inherit;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default),
              background var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.resource-list-filter-bar__filter-trigger:hover {
  border-color: var(--color-primary);
  background: var(--color-bg-hover);
}

.resource-list-filter-bar__filter-trigger:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.resource-list-filter-bar__filter-trigger--active {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.resource-list-filter-bar__filter-icon {
  flex-shrink: 0;
}

.resource-list-filter-bar__filter-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 1.1rem;
  height: 1.1rem;
  padding: 0 0.3rem;
  border-radius: 999px;
  background: var(--color-primary);
  color: var(--color-on-primary, #fff);
  font-size: 0.6875rem;
  font-weight: 600;
  line-height: 1;
}

.resource-list-filter-bar__panel {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: var(--z-dropdown);
  width: min(18rem, 80vw);
  padding: 0.75rem;
  background: var(--color-bg-surface-raised);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
}

.resource-list-filter-bar__panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.resource-list-filter-bar__panel-title {
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--color-text-primary);
}

.resource-list-filter-bar__reset {
  border: none;
  background: transparent;
  color: var(--color-primary);
  font-size: 0.75rem;
  font-family: inherit;
  cursor: pointer;
  padding: 0.15rem 0.25rem;
}

.resource-list-filter-bar__reset:disabled {
  color: var(--color-text-muted);
  cursor: default;
}

.resource-list-filter-bar__reset--inline {
  align-self: center;
}

.resource-list-filter-bar__panel-field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.resource-list-filter-bar__select {
  min-height: 34px;
  width: 100%;
  padding: 0.4rem 0.65rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.875rem;
  font-family: inherit;
  outline: none;
  box-sizing: border-box;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.resource-list-filter-bar__select:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.resource-list-filter-bar__multi {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}

.resource-list-filter-bar__multi-empty {
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.resource-list-filter-bar__chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem;
  flex: 1 1 100%;
  min-width: 0;
}

.resource-list-filter-bar__chip {
  display: inline-flex;
  align-items: center;
  min-height: 26px;
  padding: 0.2rem 0.65rem;
  border-radius: var(--radius-full);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default),
              background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.resource-list-filter-bar__chip:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
}

.resource-list-filter-bar__chip:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.resource-list-filter-bar__chip--active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.resource-list-filter-bar__chip--panel {
  min-height: 24px;
  padding: 0.15rem 0.55rem;
}

.resource-list-filter-bar__conditions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem;
  flex: 1 1 100%;
  min-width: 0;
}

.resource-list-filter-bar__condition {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  min-height: 24px;
  padding: 0.15rem 0.55rem;
  border-radius: var(--radius-full);
  border: 1px solid var(--color-primary);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-size: 0.75rem;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default),
              color var(--duration-fast) var(--ease-default);
}

.resource-list-filter-bar__condition:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.resource-list-filter-bar__condition:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
}

.resource-list-filter-bar__condition-label {
  max-width: 16rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 768px) {
  .resource-list-filter-bar__field--agent,
  .resource-list-filter-bar__field--search {
    width: 100%;
    flex: 1 1 100%;
    max-width: none;
  }

  .resource-list-filter-bar__panel {
    left: auto;
    right: 0;
  }

  .resource-list-filter-bar__chips {
    flex-wrap: nowrap;
    overflow-x: auto;
    padding-bottom: 0.15rem;
  }
}
</style>
