<template>
  <OperationTracker
    v-for="operation in trackedOperations"
    :key="`tracker-${operation.operation_id}`"
    :operation-id="operation.operation_id"
  />
  <aside v-if="visibleOperations.length || actionError" class="operation-list" aria-label="配置生效状态">
    <p v-if="actionError" class="operation-list__error" role="alert">{{ actionError }}</p>
    <OperationStatus
      v-for="operation in visibleOperations"
      :key="operation.operation_id"
      :operation="operation"
      :busy="busyID === operation.operation_id"
      :agent-name-by-id="agentNameById"
      @retry="retry"
      @rollback="rollback"
    />
  </aside>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useAgents } from '../../hooks/useAgents'
import { useOperationsStore } from '../../stores/operations'
import OperationStatus from './OperationStatus.vue'
import OperationTracker from './OperationTracker.vue'

const props = defineProps({
  agentId: { type: String, default: '' }
})

const store = useOperationsStore()
const operations = store.operations
const { data: agentsData } = useAgents()
const agentNameById = computed(() => {
  const names = new Map()
  for (const agent of agentsData.value || []) {
    const id = String(agent?.id || '').trim()
    const name = String(agent?.name || '').trim()
    if (id && name) names.set(id, name)
  }
  return names
})
const trackedOperations = computed(() => operations.value.filter((operation) => !operation.terminal))
const visibleOperations = computed(() => operations.value
  .filter(matchesSelectedAgent)
  .filter((operation) => !operation.terminal || ['failed', 'degraded'].includes(operation.ui_status))
  .filter((operation) => !(operation.ui_status === 'draining' && operation.completed_at))
  .slice(0, 5))
const busyID = ref('')
const actionError = ref('')

function matchesSelectedAgent(operation) {
  const selectedAgentID = String(props.agentId || '').trim()
  if (!selectedAgentID) return true
  if (String(operation.agent_id || '') === selectedAgentID) return true
  return operation.agents?.some((agent) => String(agent?.agent_id || '') === selectedAgentID) || false
}

async function retry(target) {
  if (!window.confirm('确认重试这个 revision？')) return
  busyID.value = target.operationID
  actionError.value = ''
  try {
    await store.retry(target.operationID, target.agentID)
  } catch (error) {
    actionError.value = error.message || '重试失败'
  } finally {
    busyID.value = ''
  }
}

async function rollback(target) {
  if (!window.confirm('确认回滚到上次可用版本？')) return
  busyID.value = target.operationID
  actionError.value = ''
  try {
    await store.rollback(target.operationID, target.agentID)
  } catch (error) {
    actionError.value = error.message || '回滚失败'
  } finally {
    busyID.value = ''
  }
}
</script>

<style scoped>
.operation-list { display: grid; gap: 0.5rem; margin-bottom: 1rem; }
.operation-list__error { color: var(--color-danger); margin: 0; }
</style>
