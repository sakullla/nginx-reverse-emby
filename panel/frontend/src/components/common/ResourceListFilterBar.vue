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
        v-if="hasStatusFields"
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
              @click="resetStatusFilters"
            >
              重置
            </button>
          </div>

          <div
            v-for="field in statusFields"
            :key="field.key"
            class="resource-list-filter-bar__panel-field"
          >
            <label class="resource-list-filter-bar__label">{{ field.label || field.key }}</label>
            <select
              class="resource-list-filter-bar__select"
              :value="statusValues[field.key] ?? field.defaultValue ?? ''"
              :aria-label="field.label || field.key"
              @change="onStatusChange(field.key, $event.target.value)"
            >
              <option
                v-for="option in field.options || []"
                :key="`${field.key}:${option.value}`"
                :value="option.value"
              >
                {{ option.label }}
              </option>
            </select>
          </div>
        </div>
      </div>

      <slot name="extra" />
    </div>

    <div v-if="$slots.actions" class="resource-list-filter-bar__actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AgentSearchSelect from './AgentSearchSelect.vue'

const props = defineProps({
  agentId: { type: String, default: '' },
  agents: { type: Array, default: () => [] },
  q: { type: String, default: '' },
  showSearch: { type: Boolean, default: true },
  searchPlaceholder: { type: String, default: '搜索...' },
  showLabels: { type: Boolean, default: false },
  /**
   * Optional status/enabled fields, e.g.
   * [{ key: 'enabled', label: '启用状态', options: [{ value: '', label: '全部' }, ...] }]
   */
  statusFields: { type: Array, default: () => [] },
  /** Map of field.key -> current string value */
  statusValues: { type: Object, default: () => ({}) }
})

const emit = defineEmits([
  'update:agentId',
  'update:q',
  'update:status',
  'change'
])

const panelOpen = ref(false)
const filterRootRef = ref(null)

const hasStatusFields = computed(() => Array.isArray(props.statusFields) && props.statusFields.length > 0)
const hasSearchQuery = computed(() => String(props.q || '').length > 0)

const activeFilterCount = computed(() => {
  if (!hasStatusFields.value) return 0
  return props.statusFields.reduce((count, field) => {
    const current = props.statusValues?.[field.key]
    const resolved = current === undefined || current === null
      ? (field.defaultValue ?? '')
      : current
    const baseline = field.defaultValue ?? ''
    return String(resolved) === String(baseline) ? count : count + 1
  }, 0)
})

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

function onStatusChange(key, value) {
  emit('update:status', { key, value })
  emit('change', { type: 'status', key, value })
}

function togglePanel() {
  panelOpen.value = !panelOpen.value
}

function closePanel() {
  panelOpen.value = false
}

function resetStatusFilters() {
  for (const field of props.statusFields || []) {
    const baseline = field.defaultValue ?? ''
    const current = props.statusValues?.[field.key]
    const resolved = current === undefined || current === null ? baseline : current
    if (String(resolved) === String(baseline)) continue
    onStatusChange(field.key, baseline)
  }
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
  justify-content: space-between;
  gap: 0.5rem 0.65rem;
  margin-bottom: 0.875rem;
}

.resource-list-filter-bar__toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.45rem;
  flex: 1 1 auto;
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
  width: min(16rem, 80vw);
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

.resource-list-filter-bar__actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex: 0 0 auto;
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
}
</style>
