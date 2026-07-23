<template>
  <section class="operation-status" :data-status="operation.ui_status" :data-tone="tone" role="status" aria-live="polite">
    <div class="operation-status__main">
      <span class="operation-status__icon" aria-hidden="true">
        <span :class="iconClass" />
      </span>
      <div class="operation-status__text">
        <span class="operation-status__label">{{ statusLabel }}</span>
        <span v-if="metaItems.length" class="operation-status__meta">
          <template v-for="(item, index) in metaItems" :key="item.text">
            <span v-if="index > 0" class="operation-status__sep" aria-hidden="true">·</span>
            <span :class="{ 'operation-status__meta--mono': item.mono }">{{ item.text }}</span>
          </template>
        </span>
      </div>
      <div v-if="canRecover && !failedAgents.length" class="operation-status__actions">
        <button type="button" class="operation-status__btn operation-status__btn--solid" :disabled="busy" @click="emitRecovery('retry')">重试</button>
        <button type="button" class="operation-status__btn" :disabled="busy" @click="emitRecovery('rollback')">回滚到上次可用版本</button>
      </div>
    </div>
    <p v-if="operation.error_message" class="operation-status__error">
      {{ operation.error_code ? `${operation.error_code}: ` : '' }}{{ operation.error_message }}
    </p>
    <ul v-if="failedAgents.length" class="operation-status__agents" aria-label="失败节点">
      <li v-for="agent in failedAgents" :key="agent.agent_id" class="operation-status__agent">
        <div class="operation-status__agent-info">
          <strong>{{ agentLabel(agent.agent_id, agent.agent_name) }}</strong>
          <span v-if="agent.desired_revision" class="operation-status__meta--mono">revision {{ agent.desired_revision }}</span>
          <span v-if="agent.attempt_count">第 {{ agent.attempt_count }} 次尝试</span>
          <span v-if="agent.error_message" class="operation-status__error">
            {{ agent.error_code ? `${agent.error_code}: ` : '' }}{{ agent.error_message }}
          </span>
        </div>
        <div class="operation-status__actions">
          <button type="button" class="operation-status__btn operation-status__btn--solid" :disabled="busy" @click="emitRecovery('retry', agent)">重试</button>
          <button type="button" class="operation-status__btn" :disabled="busy" @click="emitRecovery('rollback', agent)">回滚到上次可用版本</button>
        </div>
      </li>
    </ul>
  </section>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  operation: { type: Object, required: true },
  busy: { type: Boolean, default: false },
  agentNameById: { type: Map, default: () => new Map() }
})

const emit = defineEmits(['retry', 'rollback'])

const labels = {
  pending: '已保存，等待生效',
  applying: '正在生效',
  applied: '已生效',
  draining: '已生效，旧连接排空中',
  drained: '已生效，旧连接已排空',
  forced: '已生效，旧连接被强制关闭',
  failed: '生效失败',
  degraded: '部分节点生效失败',
  superseded: '已被更新版本替代'
}

const tones = {
  pending: 'progress',
  applying: 'progress',
  applied: 'success',
  draining: 'progress',
  drained: 'success',
  forced: 'success',
  failed: 'danger',
  degraded: 'warning',
  superseded: 'neutral'
}

const icons = {
  progress: 'i-mdi-sync operation-status__icon--spin',
  success: 'i-mdi-check-circle-outline',
  danger: 'i-mdi-close-circle-outline',
  warning: 'i-mdi-alert-circle-outline',
  neutral: 'i-mdi-history'
}

const statusLabel = computed(() => labels[props.operation.ui_status] || labels.pending)
const tone = computed(() => tones[props.operation.ui_status] || 'progress')
const iconClass = computed(() => icons[tone.value])
const canRecover = computed(() => ['failed', 'degraded'].includes(props.operation.ui_status))
const failedAgents = computed(() => props.operation.agents?.filter((agent) => agent.apply_status === 'failed') || [])
const attemptLabel = computed(() => {
  const attempts = props.operation.agents?.reduce((max, agent) => Math.max(max, Number(agent.attempt_count) || 0), 0) || 0
  return attempts > 0 ? `第 ${attempts} 次尝试` : ''
})
const metaItems = computed(() => {
  const items = []
  if (props.operation.agent_id) items.push({ text: agentLabel(props.operation.agent_id, props.operation.agent_name) })
  if (props.operation.desired_revision) items.push({ text: `revision ${props.operation.desired_revision}`, mono: true })
  if (attemptLabel.value) items.push({ text: attemptLabel.value })
  return items
})

function agentLabel(agentID, agentName = '') {
  const id = String(agentID || '').trim()
  return props.agentNameById.get(id) || String(agentName || '').trim() || id
}

function emitRecovery(action, agent = {}) {
  emit(action, {
    operationID: props.operation.operation_id,
    agentID: agent.agent_id || '',
    revision: agent.desired_revision || props.operation.desired_revision || 0
  })
}
</script>

<style scoped>
.operation-status {
  padding: var(--space-2-5) var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  overflow-wrap: anywhere;
}

.operation-status[data-tone='success'] { background: var(--color-success-subtle); border-color: var(--color-success-glow); }
.operation-status[data-tone='progress'] { background: var(--color-primary-subtle); border-color: var(--color-primary-200); }
.operation-status[data-tone='warning'] { background: var(--color-warning-subtle); border-color: var(--color-warning-glow); }
.operation-status[data-tone='danger'] { background: var(--color-danger-subtle); border-color: var(--color-danger-glow); }
.operation-status[data-tone='neutral'] { background: var(--color-bg-subtle); }

.operation-status__main { display: flex; align-items: center; gap: var(--space-2-5); }

.operation-status__icon { display: inline-flex; font-size: 1.1rem; line-height: 1; flex-shrink: 0; }
.operation-status[data-tone='success'] .operation-status__icon { color: var(--color-success); }
.operation-status[data-tone='progress'] .operation-status__icon { color: var(--color-primary); }
.operation-status[data-tone='warning'] .operation-status__icon { color: var(--color-warning); }
.operation-status[data-tone='danger'] .operation-status__icon { color: var(--color-danger); }
.operation-status[data-tone='neutral'] .operation-status__icon { color: var(--color-text-tertiary); }

.operation-status__icon--spin { display: inline-block; animation: operation-status-spin 1.4s linear infinite; }
@keyframes operation-status-spin { to { transform: rotate(360deg); } }

.operation-status__text { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-1) var(--space-2); min-width: 0; }

.operation-status__label { font-size: var(--text-sm); font-weight: var(--font-semibold); color: var(--color-text-primary); }
.operation-status[data-tone='success'] .operation-status__label { color: var(--color-success); }
.operation-status[data-tone='progress'] .operation-status__label { color: var(--color-primary); }
.operation-status[data-tone='warning'] .operation-status__label { color: var(--color-warning); }
.operation-status[data-tone='danger'] .operation-status__label { color: var(--color-danger); }

.operation-status__meta { display: inline-flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-1-5); font-size: var(--text-xs); color: var(--color-text-tertiary); }
.operation-status__sep { color: var(--color-text-muted); }
.operation-status__meta--mono { font-family: var(--font-mono); }

.operation-status__error { color: var(--color-danger); font-size: var(--text-xs); margin: var(--space-1-5) 0 0 calc(1.1rem + var(--space-2-5)); }

.operation-status__actions { display: flex; flex-wrap: wrap; gap: var(--space-1-5); align-items: center; margin-left: auto; }

.operation-status__btn {
  padding: 3px 10px;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-full);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  font-family: inherit;
  cursor: pointer;
  white-space: nowrap;
  transition: all var(--duration-fast) var(--ease-default);
}
.operation-status__btn:hover:not(:disabled) { border-color: var(--color-primary); color: var(--color-primary); }
.operation-status__btn:disabled { opacity: 0.5; cursor: not-allowed; }

.operation-status__btn--solid { background: var(--color-primary); border-color: var(--color-primary); color: var(--color-text-inverse); }
.operation-status__btn--solid:hover:not(:disabled) { background: var(--color-primary-hover); color: var(--color-text-inverse); }

.operation-status__agents { display: grid; gap: var(--space-2); list-style: none; margin: var(--space-2) 0 0; padding: 0; }
.operation-status__agent { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; padding-top: var(--space-2); border-top: 1px solid var(--color-border-subtle); }
.operation-status__agent-info { display: flex; flex-wrap: wrap; gap: var(--space-1-5); align-items: baseline; font-size: var(--text-xs); color: var(--color-text-secondary); min-width: 0; flex: 1; }
.operation-status__agent-info strong { color: var(--color-text-primary); }
.operation-status__agent-info .operation-status__error { margin: 0; }
</style>
