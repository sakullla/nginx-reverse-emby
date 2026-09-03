import { computed, onScopeDispose, ref, watch } from 'vue'
import { fetchPkiOperationStatus, normalizePkiOperation, safePkiOperationStatusURL } from '../api/pki'

export const PKI_OPERATION_STORAGE_KEY = 'nre.pki.operations.v1'

const trackedOperations = ref([])
const operationErrors = ref({})
let restored = false
let operationGeneration = 0

function browserStorage() {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

export function persistedPkiOperation(operation) {
  const normalized = normalizePkiOperation(operation)
  return {
    id: normalized.id,
    operation_id: normalized.id,
    status_url: safePkiOperationStatusURL(normalized.status_url),
    kind: normalized.kind,
    target_type: normalized.target_type,
    target_id: normalized.target_id,
    state: normalized.state,
    phase: normalized.phase,
    created_at: normalized.created_at,
    updated_at: normalized.updated_at,
    last_error: normalized.last_error
  }
}

function persist(storage = browserStorage()) {
  if (!storage) return
  try {
    const safe = trackedOperations.value.slice(0, 20).map(persistedPkiOperation)
    storage.setItem(PKI_OPERATION_STORAGE_KEY, JSON.stringify(safe))
  } catch {
    // Operation recovery is best effort; the canonical status remains server-side.
  }
}

export function restorePkiOperations(storage = browserStorage()) {
  if (!storage) return trackedOperations.value
  try {
    const parsed = JSON.parse(storage.getItem(PKI_OPERATION_STORAGE_KEY) || '[]')
    trackedOperations.value = Array.isArray(parsed)
      ? parsed.map(normalizePkiOperation).filter(operation => operation.id).slice(0, 20)
      : []
  } catch {
    trackedOperations.value = []
    try {
      storage.removeItem(PKI_OPERATION_STORAGE_KEY)
    } catch {
      // Ignore unavailable browser storage.
    }
  }
  restored = true
  persist(storage)
  return trackedOperations.value
}

function mergeOperation(current, incoming) {
  const next = normalizePkiOperation(incoming)
  if (!current) return next
  return normalizePkiOperation({
    ...current,
    ...next,
    result: next.result || current.result
  })
}

export function recordPkiOperation(operation, storage = browserStorage()) {
  const normalized = normalizePkiOperation(operation)
  if (!normalized.id) return null
  const current = trackedOperations.value.find(item => item.id === normalized.id)
  const merged = mergeOperation(current, normalized)
  trackedOperations.value = [merged, ...trackedOperations.value.filter(item => item.id !== merged.id)].slice(0, 20)
  const nextErrors = { ...operationErrors.value }
  delete nextErrors[merged.id]
  operationErrors.value = nextErrors
  persist(storage)
  return merged
}

export function forgetPkiOperation(operationID, storage = browserStorage()) {
  const id = String(operationID || '').trim()
  trackedOperations.value = trackedOperations.value.filter(item => item.id !== id)
  const nextErrors = { ...operationErrors.value }
  delete nextErrors[id]
  operationErrors.value = nextErrors
  persist(storage)
}

export function resetPkiOperationMemory(storage = browserStorage()) {
  operationGeneration += 1
  trackedOperations.value = []
  operationErrors.value = {}
  restored = false
  try {
    storage?.removeItem(PKI_OPERATION_STORAGE_KEY)
  } catch {
    // Ignore unavailable browser storage.
  }
}

function operationError(error) {
  const status = Number(error?.status ?? error?.response?.status) || 0
  return {
    status,
    message: String(error?.message || '内部 PKI 操作状态暂时不可用'),
    recoverable: status === 0 || [404, 409, 503].includes(status)
  }
}

export function usePkiOperations(options = {}) {
  const interval = Number(options.pollInterval) || 2000
  let timer = null
  let polling = false

  if (!restored) restorePkiOperations(options.storage || browserStorage())

  const operations = computed(() => trackedOperations.value)
  const errors = computed(() => operationErrors.value)
  const activeOperations = computed(() => trackedOperations.value.filter(operation => !operation.terminal))

  function stopPolling() {
    if (timer) clearTimeout(timer)
    timer = null
  }

  function schedule() {
    stopPolling()
    if (interval < 0 || activeOperations.value.length === 0) return
    timer = setTimeout(async () => {
      await refreshActive()
      schedule()
    }, interval)
  }

  async function refresh(operationID) {
    const id = String(operationID || '').trim()
    const current = trackedOperations.value.find(operation => operation.id === id)
    if (!current) return null
    const generation = operationGeneration
    try {
      const next = await fetchPkiOperationStatus(current.status_url || current.id)
      if (generation !== operationGeneration) return null
      return recordPkiOperation(next, options.storage || browserStorage())
    } catch (error) {
      if (generation !== operationGeneration) return null
      operationErrors.value = { ...operationErrors.value, [id]: operationError(error) }
      return current
    }
  }

  async function refreshActive() {
    if (polling) return activeOperations.value
    polling = true
    try {
      await Promise.all(activeOperations.value.map(operation => refresh(operation.id)))
      return activeOperations.value
    } finally {
      polling = false
    }
  }

  function track(operation) {
    return recordPkiOperation(operation, options.storage || browserStorage())
  }

  function forget(operationID) {
    forgetPkiOperation(operationID, options.storage || browserStorage())
  }

  watch(
    () => activeOperations.value.map(operation => `${operation.id}:${operation.state}:${operation.updated_at}`).join('|'),
    schedule,
    { immediate: true }
  )
  onScopeDispose(stopPolling)

  if (activeOperations.value.length > 0 && options.refreshOnRestore !== false) {
    Promise.resolve().then(refreshActive)
  }

  return {
    operations,
    activeOperations,
    errors,
    track,
    refresh,
    refreshActive,
    forget,
    stopPolling
  }
}
