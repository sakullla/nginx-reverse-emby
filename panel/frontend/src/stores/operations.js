import { computed, reactive } from 'vue'
import { fetchOperationStatus, normalizeOperationStatus, retryRevision, rollbackRevision } from '../api/operations'

const STORAGE_KEY = 'nre.operations.v1'
const MAX_OPERATIONS = 50
const state = reactive({ byId: {}, order: [] })
const refreshSequence = new Map()

function persist() {
  if (typeof localStorage === 'undefined') return
  const values = state.order.map((id) => state.byId[id]).filter(Boolean)
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(values))
  } catch {
    // Tracking must never turn a successful configuration mutation into a UI error.
  }
}

function mergeOperation(current, next) {
  return normalizeOperationStatus({ ...current, ...next, agents: next.agents?.length ? next.agents : current?.agents })
}

export function recordAcceptedOperation(operation) {
  if (!operation?.operation_id) return null
  const id = operation.operation_id
  state.byId[id] = mergeOperation(state.byId[id], operation)
  state.order = [id, ...state.order.filter((item) => item !== id)].slice(0, MAX_OPERATIONS)
  persist()
  return state.byId[id]
}

export function trackMutationResult(result) {
  return recordAcceptedOperation(result?.operation)
}

export function restoreOperations() {
  if (typeof localStorage === 'undefined') return []
  try {
    const values = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]')
    if (!Array.isArray(values)) throw new Error('invalid operation storage')
    state.byId = {}
    state.order = []
    values.slice(0, MAX_OPERATIONS).forEach((value) => {
      const operation = mergeOperation(null, value)
      if (!operation.operation_id || state.byId[operation.operation_id]) return
      state.byId[operation.operation_id] = operation
      state.order.push(operation.operation_id)
    })
    persist()
  } catch {
    state.byId = {}
    state.order = []
    try { localStorage.removeItem(STORAGE_KEY) } catch { /* storage is best effort */ }
  }
  return state.order.map((id) => state.byId[id])
}

export function resetOperations() {
  state.byId = {}
  state.order = []
  refreshSequence.clear()
  if (typeof localStorage !== 'undefined') {
    try { localStorage.removeItem(STORAGE_KEY) } catch { /* storage is best effort */ }
  }
}

export async function refreshOperation(operationID) {
  const current = state.byId[operationID]
  if (!current?.status_url) return current || null
  const sequence = (refreshSequence.get(operationID) || 0) + 1
  refreshSequence.set(operationID, sequence)
  const next = await fetchOperationStatus(current.status_url)
  if (refreshSequence.get(operationID) !== sequence) return state.byId[operationID] || null
  next.status_url ||= current.status_url
  return recordAcceptedOperation(next)
}

function operationAgent(operation, agentID) {
  return operation?.agents?.find((agent) => agent.agent_id === agentID)
}

export async function retryOperation(operationID, agentID = '') {
  const current = state.byId[operationID]
  if (!current) return null
  return recordAcceptedOperation(await retryRevision(current, operationAgent(current, agentID)))
}

export async function rollbackOperation(operationID, agentID = '') {
  const current = state.byId[operationID]
  if (!current) return null
  return recordAcceptedOperation(await rollbackRevision(current, operationAgent(current, agentID)))
}

const operationsStore = {
  operations: computed(() => state.order.map((id) => state.byId[id]).filter(Boolean)),
  get: (id) => state.byId[id] || null,
  record: recordAcceptedOperation,
  track: trackMutationResult,
  refresh: refreshOperation,
  retry: retryOperation,
  rollback: rollbackOperation,
  restore: restoreOperations,
  reset: resetOperations
}

export function useOperationsStore() { return operationsStore }

restoreOperations()
