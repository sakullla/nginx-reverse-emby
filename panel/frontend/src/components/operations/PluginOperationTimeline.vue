<script setup>
import { computed } from 'vue'
import { safePluginJSON, sanitizePluginText } from '../../api/pluginSecurity'
import { formatPanelDateTime, panelTimeZone } from '../../utils/panelDateTime.js'
import BaseBadge from '../base/BaseBadge.vue'

const props = defineProps({ operations: { type: Array, default: () => [] } })

const visibleOperations = computed(() => [...props.operations]
  .sort((left, right) => {
    const leftTime = Date.parse(left?.created_at || '') || 0
    const rightTime = Date.parse(right?.created_at || '') || 0
    if (leftTime !== rightTime) return rightTime - leftTime
    return String(right?.id || '').localeCompare(String(left?.id || ''))
  })
  .slice(0, 5))

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
        <strong>{{ operation.kind }}</strong>
        <BaseBadge :tone="statusTone(operation.status)">{{ operation.status }}</BaseBadge>
        <time :datetime="operation.completed_at || operation.created_at" :title="operation.completed_at || operation.created_at" :data-timezone="panelTimeZone">{{ formatStamp(operation.completed_at || operation.created_at) }}</time>
      </div>
      <p>操作人 {{ operation.actor_id || 'system' }} · revision {{ operation.target_revision || '—' }}</p>
      <p v-if="operation.error" class="plugin-operation-timeline__error">{{ sanitizePluginText(operation.error) }}</p>
      <details v-if="operation.agent_results && Object.keys(operation.agent_results).length">
        <summary>逐 Agent 安全回报</summary>
        <pre>{{ safePluginJSON(operation.agent_results) }}</pre>
      </details>
    </li>
  </ol>
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
  border-radius: 999px;
  background: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-subtle);
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
