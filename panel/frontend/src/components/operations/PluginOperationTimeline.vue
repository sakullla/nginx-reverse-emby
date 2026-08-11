<script setup>
import { safePluginJSON, sanitizePluginText } from '../../api/pluginSecurity'

defineProps({ operations: { type: Array, default: () => [] } })
</script>

<template>
  <ol class="plugin-operation-timeline">
    <li v-for="operation in operations" :key="operation.id">
      <div class="plugin-operation-timeline__heading">
        <strong>{{ operation.kind }}</strong>
        <span>{{ operation.status }}</span>
        <time>{{ operation.completed_at || operation.created_at }}</time>
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
.plugin-operation-timeline { display: grid; gap: var(--space-3); margin: 0; padding: 0; list-style: none; }
.plugin-operation-timeline li { padding: var(--space-4); border-left: 3px solid var(--color-border-default); background: var(--color-bg-subtle); }
.plugin-operation-timeline__heading { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: baseline; }
.plugin-operation-timeline__heading span { color: var(--color-primary); }
time, p { color: var(--color-text-muted); font-size: var(--text-xs); }
time { margin-left: auto; }
.plugin-operation-timeline__error { color: var(--color-danger); }
summary { cursor: pointer; font-size: var(--text-sm); }
pre { overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font-size: var(--text-xs); }
</style>
