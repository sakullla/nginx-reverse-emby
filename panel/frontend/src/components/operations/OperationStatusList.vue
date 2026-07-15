<template>
  <aside v-if="operations.length" class="operation-list" aria-label="配置生效状态">
    <p v-if="actionError" class="operation-list__error" role="alert">{{ actionError }}</p>
    <OperationStatus
      v-for="operation in operations.slice(0, 5)"
      :key="operation.operation_id"
      :operation="operation"
      :busy="busyID === operation.operation_id"
      @retry="retry"
      @rollback="rollback"
    />
  </aside>
</template>

<script setup>
import { ref } from 'vue'
import { useOperationsStore } from '../../stores/operations'
import OperationStatus from './OperationStatus.vue'

const store = useOperationsStore()
const operations = store.operations
const busyID = ref('')
const actionError = ref('')

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
