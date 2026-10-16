<script setup>
import { computed, ref } from 'vue'
import { safePluginJSON, sanitizePluginText } from '../../api/pluginSecurity'
import { formatPanelDateTime, panelTimeZone } from '../../utils/panelDateTime.js'
import { pluginOperationKindLabel, pluginOperationStatusLabel } from '../../utils/pluginOperationLabels.js'
import BaseBadge from '../base/BaseBadge.vue'

const COLLAPSED_OPERATION_COUNT = 5

const props = defineProps({ operations: { type: Array, default: () => [] } })

const expanded = ref(false)

const sortedOperations = computed(() => [...props.operations]
  .sort((left, right) => {
    const leftTime = Date.parse(left?.created_at || '') || 0
    const rightTime = Date.parse(right?.created_at || '') || 0
    if (leftTime !== rightTime) return rightTime - leftTime
    return String(right?.id || '').localeCompare(String(left?.id || ''))
  }))

const visibleOperations = computed(() => expanded.value
  ? sortedOperations.value
  : sortedOperations.value.slice(0, COLLAPSED_OPERATION_COUNT))

const hiddenCount = computed(() => Math.max(sortedOperations.value.length - COLLAPSED_OPERATION_COUNT, 0))

function statusTone(status) {
  const value = String(status || '').toLowerCase()
  if (['failed', 'error', 'cancelled', 'canceled', 'rejected'].includes(value)) return 'danger'
  if (['succeeded', 'success', 'completed', 'applied'].includes(value)) return 'success'
  return 'warning'
}

function formatStamp(value) {
  return formatPanelDateTime(value, '')
}
</script>

<template>
  <p v-if="!visibleOperations.length" class="plugin-operation-timeline__empty">暂无生命周期操作记录。</p>
  <ol v-else class="plugin-operation-timeline">
    <li v-for="operation in visibleOperations" :key="operation.id">
      <div class="plugin-operation-timeline__heading">
        <strong :title="operation.kind">{{ pluginOperationKindLabel(operation.kind) }}</strong>
        <BaseBadge :tone="statusTone(operation.status)" :title="operation.status">{{ pluginOperationStatusLabel(operation.status) }}</BaseBadge>
        <time :datetime="operation.completed_at || operation.created_at" :title="operation.completed_at || operation.created_at" :data-timezone="panelTimeZone">{{ formatStamp(operation.completed_at || operation.created_at) }}</time>
      </div>
      <p>操作人 {{ operation.actor_id || 'system' }} · revision {{ operation.target_revision || '—' }}</p>
      <p v-if="operation.error" class="plugin-operation-timeline__error">{{ sanitizePluginText(operation.error) }}</p>
      <details v-if="operation.agent_results && Object.keys(operation.agent_results).length">
        <summary>查看各节点执行结果</summary>
        <pre>{{ safePluginJSON(operation.agent_results) }}</pre>
      </details>
    </li>
  </ol>
  <button
    v-if="hiddenCount"
    type="button"
    class="plugin-operation-timeline__more"
    :aria-expanded="expanded"
    data-test="plugin-operation-toggle"
    @click="expanded = !expanded"
  >
    {{ expanded ? '收起操作记录' : `查看更多操作记录（还有 ${hiddenCount} 条）` }}
  </button>
</template>

<style scoped>
.plugin-operation-timeline {
  display: grid;
  gap: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}

.plugin-operation-timeline__empty {
  margin: 0;
  padding: 1.1rem 0.5rem;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  text-align: center;
}

.plugin-operation-timeline li {
  position: relative;
  display: grid;
  gap: 0.35rem;
  min-width: 0;
  margin: 0;
  padding: 0.85rem 0.9rem 0.85rem 1.35rem;
  border: 1px solid var(--color-border-subtle);
  border-bottom-width: 0;
  background: var(--color-bg-surface);
}

.plugin-operation-timeline li:first-child {
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
}

.plugin-operation-timeline li:last-child {
  border-bottom-width: 1px;
  border-radius: 0 0 var(--radius-xl) var(--radius-xl);
}

.plugin-operation-timeline li:only-child {
  border-radius: var(--radius-xl);
}

.plugin-operation-timeline li::before {
  content: '';
  position: absolute;
  left: 0.55rem;
  top: 1.2rem;
  width: 0.45rem;
  height: 0.45rem;
  border-radius: var(--radius-full);
  background: var(--color-primary);
  box-shadow: var(--shadow-dot-ring);
}

.plugin-operation-timeline__heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem 0.55rem;
  min-width: 0;
}

.plugin-operation-timeline__heading strong {
  color: var(--color-text-primary);
  font-size: 0.875rem;
}

time,
p {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

p {
  margin: 0;
}

time {
  margin-left: auto;
  font-family: var(--font-mono);
}

.plugin-operation-timeline__error {
  color: var(--color-danger);
  overflow-wrap: anywhere;
}

.plugin-operation-timeline__more {
  display: block;
  width: 100%;
  margin-top: 0.6rem;
  padding: 0.55rem 0.7rem;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-lg);
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-size: var(--text-sm);
  cursor: pointer;
}

.plugin-operation-timeline__more:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
}

.plugin-operation-timeline__more:focus-visible {
  outline: none;
  box-shadow: var(--shadow-focus);
}

summary {
  cursor: pointer;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

pre {
  overflow: auto;
  margin: 0.45rem 0 0;
  padding: 0.65rem 0.75rem;
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: var(--text-xs);
}

@media (max-width: 42rem) {
  time {
    margin-left: 0;
    width: 100%;
  }
}
</style>
