<script setup>
import { safePluginJSON, sanitizePluginText } from '../../api/pluginSecurity'

defineProps({
  statuses: { type: Array, default: () => [] },
  actionable: { type: Boolean, default: false },
  busyAgent: { type: String, default: '' }
})
defineEmits(['retry'])

function statusTone(status) {
  const value = String(status.runtime_state || status.current_state || '').toLowerCase()
  if (['failed', 'degraded', 'crashed'].includes(value)) return 'danger'
  if (['active', 'ready', 'applied'].includes(value)) return 'success'
  return 'pending'
}
</script>

<template>
  <div class="agent-status-table">
    <p v-if="!statuses.length" class="agent-status-table__empty">尚无目标 Agent 运行状态。</p>
    <article v-for="status in statuses" v-else :key="`${status.instance_id}:${status.agent_id}:${status.target_scope}`" class="agent-status-card">
      <header>
        <div>
          <strong>{{ status.agent_id }}</strong>
          <small>{{ status.instance_id }} · {{ status.target_scope }}</small>
        </div>
        <span :class="`tone-${statusTone(status)}`">{{ status.runtime_state || status.current_state }}</span>
      </header>
      <dl>
        <div><dt>Revision</dt><dd>{{ status.current_revision || 0 }} / {{ status.desired_revision || status.target_revision || 0 }}</dd></div>
        <div><dt>操作</dt><dd>{{ status.operation_kind || '—' }} · {{ status.operation_status || '—' }}</dd></div>
        <div><dt>最近回报</dt><dd>{{ status.reported_at || '—' }}</dd></div>
        <div><dt>错误码</dt><dd>{{ status.runtime_error_code || '—' }}</dd></div>
      </dl>
      <p v-if="status.last_apply_message" class="agent-status-card__message">{{ sanitizePluginText(status.last_apply_message) }}</p>
      <button
        v-if="actionable && ['failed', 'degraded', 'crashed'].includes(status.runtime_state || status.current_state) && (status.desired_revision || status.target_revision)"
        class="btn btn-secondary"
        type="button"
        :disabled="busyAgent === status.agent_id"
        @click="$emit('retry', status)"
      >
        {{ busyAgent === status.agent_id ? '重试中…' : '重试此 Agent revision' }}
      </button>
      <details v-if="status.runtime_budget || status.runtime_details">
        <summary>预算、崩溃与重试详情</summary>
        <pre>{{ safePluginJSON({ budget: status.runtime_budget, runtime: status.runtime_details }) }}</pre>
      </details>
    </article>
  </div>
</template>

<style scoped>
.agent-status-table { display: grid; gap: var(--space-3); }
.agent-status-card { padding: var(--space-4); border: 1px solid var(--color-border-default); border-radius: var(--radius-lg); }
.agent-status-card header { display: flex; justify-content: space-between; gap: var(--space-3); }
.agent-status-card header div { display: grid; gap: 2px; }
small, dt, .agent-status-table__empty { color: var(--color-text-muted); font-size: var(--text-xs); }
dl { display: grid; grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr)); gap: var(--space-3); margin: var(--space-4) 0 0; }
dd { margin: 2px 0 0; font-size: var(--text-sm); overflow-wrap: anywhere; }
.tone-success { color: var(--color-success); }.tone-danger { color: var(--color-danger); }.tone-pending { color: var(--color-warning); }
.agent-status-card__message { color: var(--color-text-secondary); font-size: var(--text-sm); }
summary { cursor: pointer; color: var(--color-text-secondary); font-size: var(--text-sm); }
pre { max-height: 14rem; overflow: auto; padding: var(--space-3); border-radius: var(--radius-md); background: var(--color-bg-subtle); white-space: pre-wrap; overflow-wrap: anywhere; font-size: var(--text-xs); }
</style>
