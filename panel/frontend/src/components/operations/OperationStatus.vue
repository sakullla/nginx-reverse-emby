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
    <div v-if="canRecover" class="operation-status__actions">
      <button type="button" :disabled="busy" @click="$emit('retry', operation.operation_id)">重试</button>
      <button type="button" :disabled="busy" @click="$emit('rollback', operation.operation_id)">回滚到上次可用版本</button>
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

defineEmits(['retry', 'rollback'])

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
const attemptLabel = computed(() => {
  const attempts = props.operation.agents?.reduce((max, agent) => Math.max(max, Number(agent.attempt_count) || 0), 0) || 0
  return attempts > 0 ? `第 ${attempts} 次尝试` : ''
})
</script>

<style scoped>
.operation-status { padding: 0.75rem; border: 1px solid var(--color-border); border-radius: 0.5rem; overflow-wrap: anywhere; }
.operation-status__main, .operation-status__actions { display: flex; flex-wrap: wrap; gap: 0.5rem; align-items: center; }
.operation-status__error { color: var(--color-danger); margin: 0.5rem 0 0; }
.operation-status__actions { margin-top: 0.5rem; }
</style>
