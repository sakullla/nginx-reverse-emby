import { computed, reactive } from 'vue'
import { dismissOperationStatus, fetchOperationStatus, normalizeOperationStatus, retryRevision, rollbackRevision } from '../api/operations'

const STORAGE_KEY = 'nre.operations.v1'
const MAX_OPERATIONS = 50
const RECOVERABLE_TERMINAL_STATUSES = new Set(['failed', 'degraded'])
const state = reactive({ byId: {}, order: [] })
const refreshSequence = new Map()
let operationGeneration = 0

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

function isCompletedSuccess(operation) {
  return operation.terminal && !RECOVERABLE_TERMINAL_STATUSES.has(operation.ui_status)
}

function singleAgentRevision(operation) {
  const agents = Array.isArray(operation?.agents) ? operation.agents : []
  if (agents.length > 1) return null
  const agent = agents[0] || operation
  const agentID = String(agent?.agent_id || operation?.agent_id || '').trim()
  const revision = Number(agent?.desired_revision || operation?.desired_revision)
  if (!agentID || !Number.isSafeInteger(revision) || revision <= 0) return null
  return { agentID, revision }
}

function dropOlderSingleAgentOperations(appliedOperation) {
  if (appliedOperation.apply_status !== 'applied') return
  const applied = singleAgentRevision(appliedOperation)
  if (!applied) return
  const removed = new Set()
  state.order.forEach((id) => {
    if (id === appliedOperation.operation_id) return
    const tracked = singleAgentRevision(state.byId[id])
    if (!tracked || tracked.agentID !== applied.agentID || tracked.revision >= applied.revision) return
    delete state.byId[id]
    removed.add(id)
  })
  if (removed.size > 0) state.order = state.order.filter((id) => !removed.has(id))
}

export function recordAcceptedOperation(operation) {
  if (!operation?.operation_id) return null
  const id = operation.operation_id
  const next = mergeOperation(state.byId[id], operation)
  dropOlderSingleAgentOperations(next)
  if (isCompletedSuccess(next)) {
    delete state.byId[id]
    state.order = state.order.filter((item) => item !== id)
    persist()
    return next
  }
  state.byId[id] = next
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
      if (!operation.operation_id || isCompletedSuccess(operation) || state.byId[operation.operation_id]) return
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
  operationGeneration += 1
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
  const generation = operationGeneration
  const sequence = (refreshSequence.get(operationID) || 0) + 1
  refreshSequence.set(operationID, sequence)
  const next = await fetchOperationStatus(current.status_url)
  if (generation !== operationGeneration || refreshSequence.get(operationID) !== sequence) {
    return state.byId[operationID] || null
  }
  next.status_url ||= current.status_url
  return recordAcceptedOperation(next)
}

function operationAgent(operation, agentID) {
  return operation?.agents?.find((agent) => agent.agent_id === agentID)
}

export async function retryOperation(operationID, agentID = '') {
  const current = state.byId[operationID]
  if (!current) return null
  const generation = operationGeneration
  const next = await retryRevision(current, operationAgent(current, agentID))
  if (generation !== operationGeneration) return null
  return recordAcceptedOperation(next)
}

export async function rollbackOperation(operationID, agentID = '') {
  const current = state.byId[operationID]
  if (!current) return null
  const generation = operationGeneration
  const next = await rollbackRevision(current, operationAgent(current, agentID))
  if (generation !== operationGeneration) return null
  return recordAcceptedOperation(next)
}

export async function dismissOperation(operationID) {
  const current = state.byId[operationID]
  if (!current) return null
  const generation = operationGeneration
  const next = await dismissOperationStatus(operationID)
  if (generation !== operationGeneration) return null
  next.status_url ||= current.status_url
  delete state.byId[operationID]
  state.order = state.order.filter((item) => item !== operationID)
  refreshSequence.delete(operationID)
  persist()
  return next
}

const operationsStore = {
  operations: computed(() => state.order.map((id) => state.byId[id]).filter(Boolean)),
  get: (id) => state.byId[id] || null,
  record: recordAcceptedOperation,
  track: trackMutationResult,
  refresh: refreshOperation,
  retry: retryOperation,
  rollback: rollbackOperation,
  dismiss: dismissOperation,
  restore: restoreOperations,
  reset: resetOperations
}

export function useOperationsStore() { return operationsStore }

restoreOperations()
