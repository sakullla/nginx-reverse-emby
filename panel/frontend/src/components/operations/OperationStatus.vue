<template>
  <section class="operation-status" :data-status="operation.ui_status" role="status" aria-live="polite">
    <div class="operation-status__main">
      <strong>{{ statusLabel }}</strong>
      <span v-if="operation.agent_id">{{ operation.agent_id }}</span>
      <span v-if="operation.desired_revision">revision {{ operation.desired_revision }}</span>
      <span v-if="attemptLabel">{{ attemptLabel }}</span>
    </div>
    <p v-if="operation.error_message" class="operation-status__error">
      {{ operation.error_code ? `${operation.error_code}: ` : '' }}{{ operation.error_message }}
    </p>
    <ul v-if="failedAgents.length" class="operation-status__agents" aria-label="失败节点">
      <li v-for="agent in failedAgents" :key="agent.agent_id" class="operation-status__agent">
        <div>
          <strong>{{ agent.agent_id }}</strong>
          <span v-if="agent.desired_revision">revision {{ agent.desired_revision }}</span>
          <span v-if="agent.attempt_count">第 {{ agent.attempt_count }} 次尝试</span>
          <span v-if="agent.error_message" class="operation-status__error">
            {{ agent.error_code ? `${agent.error_code}: ` : '' }}{{ agent.error_message }}
          </span>
        </div>
        <div class="operation-status__actions">
          <button type="button" :disabled="busy" @click="emitRecovery('retry', agent)">重试</button>
          <button type="button" :disabled="busy" @click="emitRecovery('rollback', agent)">回滚到上次可用版本</button>
        </div>
      </li>
    </ul>
    <div v-else-if="canRecover" class="operation-status__actions">
      <button type="button" :disabled="busy" @click="emitRecovery('retry')">重试</button>
      <button type="button" :disabled="busy" @click="emitRecovery('rollback')">回滚到上次可用版本</button>
    </div>
  </section>
</template>

<script setup>
import { computed } from 'vue'
import { useOperationStatus } from '../../hooks/useOperationStatus'

const props = defineProps({
  operation: { type: Object, required: true },
  busy: { type: Boolean, default: false }
})

const emit = defineEmits(['retry', 'rollback'])

useOperationStatus(computed(() => props.operation.operation_id))

const labels = {
  pending: '已保存，等待生效',
  applying: '正在生效',
  applied: '已生效',
  draining: '已生效，旧连接排空中',
  drained: '已生效，旧连接已排空',
  failed: '生效失败',
  degraded: '部分节点生效失败',
  superseded: '已被更新版本替代'
}

const statusLabel = computed(() => labels[props.operation.ui_status] || labels.pending)
const canRecover = computed(() => ['failed', 'degraded'].includes(props.operation.ui_status))
const failedAgents = computed(() => props.operation.agents?.filter((agent) => agent.apply_status === 'failed') || [])
const attemptLabel = computed(() => {
  const attempts = props.operation.agents?.reduce((max, agent) => Math.max(max, Number(agent.attempt_count) || 0), 0) || 0
  return attempts > 0 ? `第 ${attempts} 次尝试` : ''
})

function emitRecovery(action, agent = {}) {
  emit(action, {
    operationID: props.operation.operation_id,
    agentID: agent.agent_id || '',
    revision: agent.desired_revision || props.operation.desired_revision || 0
  })
}
</script>

<style scoped>
.operation-status { padding: 0.75rem; border: 1px solid var(--color-border); border-radius: 0.5rem; overflow-wrap: anywhere; }
.operation-status__main, .operation-status__actions { display: flex; flex-wrap: wrap; gap: 0.5rem; align-items: center; }
.operation-status__error { color: var(--color-danger); margin: 0.5rem 0 0; }
.operation-status__actions { margin-top: 0.5rem; }
.operation-status__agents { display: grid; gap: 0.5rem; list-style: none; margin: 0.5rem 0 0; padding: 0; }
.operation-status__agent { padding-top: 0.5rem; border-top: 1px solid var(--color-border); }
.operation-status__agent > div { display: flex; flex-wrap: wrap; gap: 0.5rem; align-items: center; }
</style>
