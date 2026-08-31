<script setup>
import { safePluginJSON, sanitizePluginText } from '../../api/pluginSecurity'
import { formatPanelDateTime, panelTimeZone } from '../../utils/panelDateTime.js'
import BaseBadge from '../base/BaseBadge.vue'
import BaseListCard from '../base/BaseListCard.vue'

const props = defineProps({
  statuses: { type: Array, default: () => [] },
  agents: { type: Array, default: () => [] },
  actionable: { type: Boolean, default: false },
  busyAgent: { type: String, default: '' }
})
defineEmits(['retry', 'uninstall'])

function agentLabel(agentID) {
  const id = String(agentID || '').trim()
  const agent = agentRecord(id)
  const name = String(agent?.name || '').trim()
  return name || id
}

function agentRecord(agentID) {
  const id = String(agentID || '').trim()
  return (props.agents || []).find((item) => String(item?.id || '') === id)
}

function statusValue(status) {
  const agentState = String(agentRecord(status.agent_id)?.status || '').trim().toLowerCase()
  if (status.available === false || ['offline', 'unreachable', 'disconnected'].includes(agentState)) return 'offline'
  const runtimeState = String(status.runtime_state || '').trim().toLowerCase()
  if (isFailureState(runtimeState)) return runtimeState
  const operationStatus = String(status.operation_status || '').trim().toLowerCase()
  if (isFailureState(operationStatus)) return operationStatus
  const applyStatus = String(status.last_apply_status || '').trim().toLowerCase()
  if (isFailureState(applyStatus)) return applyStatus
  if (desiredRevision(status) > (Number(status.current_revision) || 0)) return 'unsynced'
  return runtimeState || String(status.current_state || '').trim().toLowerCase()
}

function isFailureState(value) {
  return ['failed', 'degraded', 'crashed'].includes(value) || /(?:fail|error|crash)/.test(value)
}

function statusTone(status) {
  const value = statusValue(status)
  if (value === 'offline' || isFailureState(value)) return 'danger'
  if (['active', 'ready', 'applied'].includes(value)) return 'success'
  return 'warning'
}

function desiredRevision(status) {
  return Math.max(Number(status.desired_revision) || 0, Number(status.target_revision) || 0)
}

function canRetry(status) {
  const value = statusValue(status)
  return isFailureState(value) && desiredRevision(status)
}

function statusLabel(status) {
  const value = statusValue(status)
  return {
    offline: 'Agent 离线',
    failed: '执行失败',
    degraded: '执行降级',
    crashed: '执行崩溃',
    unsynced: 'Generation 未同步',
    active: '执行面就绪',
    ready: '执行面就绪',
    applied: '执行面就绪'
  }[value] || status.runtime_state || status.current_state || '等待执行状态'
}

function syncLabel(status) {
  const value = statusValue(status)
  if (value === 'offline') return 'Agent 离线'
  if (isFailureState(value)) return '同步或启动失败'
  if (desiredRevision(status) > (Number(status.current_revision) || 0)) return 'Generation 未同步'
  if (!String(status.generation_id || '').trim()) return '等待 Generation'
  return 'Generation 已同步'
}
</script>

<template>
  <div class="agent-status-table">
    <p v-if="!statuses.length" class="agent-status-table__empty">尚无 Agent 执行面运行状态。</p>
    <BaseListCard
      v-for="status in statuses"
      v-else
      :key="`${status.instance_id}:${status.agent_id}:${status.target_scope}`"
      class="agent-status-card"
      data-test="plugin-agent-execution-status"
      :status="statusTone(status)"
      :clickable="false"
    >
      <template #header-left>
        <strong class="agent-status-card__name">{{ agentLabel(status.agent_id) }}</strong>
        <BaseBadge tone="primary">Agent 执行面</BaseBadge>
        <BaseBadge :tone="statusTone(status)" dot>{{ statusLabel(status) }}</BaseBadge>
      </template>
      <template #header-right>
        <span class="agent-status-card__scope">{{ status.instance_id }} · {{ status.target_scope }}</span>
      </template>

      <dl>
        <div>
          <dt>Generation</dt>
          <dd>{{ status.generation_id || '尚未生成' }}</dd>
        </div>
        <div>
          <dt>同步状态</dt>
          <dd>{{ syncLabel(status) }}</dd>
        </div>
        <div>
          <dt>Revision</dt>
          <dd>{{ status.current_revision || 0 }} / {{ desiredRevision(status) }}</dd>
        </div>
        <div>
          <dt>操作</dt>
          <dd>{{ status.operation_kind || '—' }} · {{ status.operation_status || '—' }}</dd>
        </div>
        <div>
          <dt>最近回报</dt>
          <dd :data-timezone="panelTimeZone">{{ formatPanelDateTime(status.reported_at) }}</dd>
        </div>
        <div>
          <dt>执行面错误码</dt>
          <dd>{{ status.runtime_error_code || '—' }}</dd>
        </div>
      </dl>

      <p
        v-if="status.last_apply_message"
        class="agent-status-card__message"
        :role="statusTone(status) === 'danger' ? 'alert' : 'status'"
      >
        <strong>Agent 执行面：</strong>{{ sanitizePluginText(status.last_apply_message) }}
      </p>

      <template #footer>
        <button
          v-if="actionable && canRetry(status)"
          class="btn btn-secondary btn-sm"
          type="button"
          :disabled="busyAgent === status.agent_id"
          @click="$emit('retry', status)"
        >
          {{ busyAgent === status.agent_id ? '重试中…' : '重试此 Agent revision' }}
        </button>
        <button
          v-if="actionable"
          class="btn btn-danger btn-sm"
          type="button"
          data-test="plugin-agent-uninstall"
          :disabled="busyAgent === status.agent_id"
          @click="$emit('uninstall', status)"
        >
          卸载执行面
        </button>
        <details v-if="status.runtime_budget || status.runtime_details" class="agent-status-card__details">
          <summary>预算、崩溃与重试详情</summary>
          <pre>{{ safePluginJSON({ budget: status.runtime_budget, runtime: status.runtime_details }) }}</pre>
        </details>
      </template>
    </BaseListCard>
  </div>
</template>

<style scoped>
.agent-status-table {
  display: grid;
  gap: var(--space-3);
}

.agent-status-table__empty {
  margin: 0;
  padding: 1.25rem 0.5rem;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  text-align: center;
}

.agent-status-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.agent-status-card :deep(.base-list-card__footer) {
  display: grid;
  gap: 0.55rem;
  padding-top: 0.55rem;
  border-top: 1px solid var(--color-border-subtle);
}

.agent-status-card__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
}

.agent-status-card__scope {
  max-width: 14rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

dl {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(8.5rem, 1fr));
  gap: 0.65rem 0.9rem;
  margin: 0.15rem 0 0;
}

dt {
  color: var(--color-text-muted);
  font-size: 0.7rem;
}

dd {
  margin: 0.15rem 0 0;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.agent-status-card__message {
  margin: 0.15rem 0 0;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.agent-status-card__details {
  min-width: 0;
}

.agent-status-card__details summary {
  cursor: pointer;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

pre {
  max-height: 14rem;
  overflow: auto;
  margin: 0.55rem 0 0;
  padding: var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: var(--text-xs);
}

.agent-status-card .btn {
  justify-self: start;
}
</style>
