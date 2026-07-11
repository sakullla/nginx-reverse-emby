<template>
  <div class="resource-list-filter-bar">
    <div class="resource-list-filter-bar__fields">
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
        <input
          class="resource-list-filter-bar__input"
          type="search"
          :value="q"
          :placeholder="searchPlaceholder"
          aria-label="搜索资源"
          @input="onSearchInput"
        />
      </div>

      <div
        v-for="field in statusFields"
        :key="field.key"
        class="resource-list-filter-bar__field"
      >
        <label v-if="showLabels" class="resource-list-filter-bar__label">{{ field.label }}</label>
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

      <slot name="extra" />
    </div>

    <div v-if="$slots.actions" class="resource-list-filter-bar__actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<script setup>
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

function onAgentUpdate(value) {
  emit('update:agentId', value)
  emit('change', { type: 'agentId', value })
}

function onSearchInput(event) {
  const value = event?.target?.value ?? ''
  emit('update:q', value)
  emit('change', { type: 'q', value })
}

function onStatusChange(key, value) {
  emit('update:status', { key, value })
  emit('change', { type: 'status', key, value })
}
</script>

<style scoped>
.resource-list-filter-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 0.75rem 1rem;
  margin-bottom: 1.25rem;
}

.resource-list-filter-bar__fields {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 0.625rem 0.75rem;
  flex: 1 1 auto;
  min-width: 0;
}

.resource-list-filter-bar__field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 140px;
}

.resource-list-filter-bar__field--agent {
  min-width: 200px;
  flex: 0 1 240px;
}

.resource-list-filter-bar__field--search {
  min-width: 180px;
  flex: 1 1 220px;
}

.resource-list-filter-bar__label {
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.resource-list-filter-bar__input,
.resource-list-filter-bar__select {
  min-height: 38px;
  padding: 0.45rem 0.75rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.875rem;
  font-family: inherit;
  outline: none;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.resource-list-filter-bar__input {
  width: 100%;
}

.resource-list-filter-bar__select {
  min-width: 140px;
  cursor: pointer;
}

.resource-list-filter-bar__input:focus,
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
  .resource-list-filter-bar__field,
  .resource-list-filter-bar__field--agent,
  .resource-list-filter-bar__field--search {
    min-width: 100%;
    flex: 1 1 100%;
  }

  .resource-list-filter-bar__select {
    width: 100%;
  }
}
</style>
