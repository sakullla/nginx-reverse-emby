import { computed, onScopeDispose, ref, unref, watch } from 'vue'
import { fetchRevisionEvents } from '../api/operations'
import { useOperationsStore } from '../stores/operations'

export function useOperationStatus(operationID, options = {}) {
  const store = useOperationsStore()
  const error = ref(null)
  const refreshing = ref(false)
  const interval = Number(options.pollInterval) || 2000
  const eventLimit = Number(options.eventLimit) || 100
  let cursor = 0
  let timer = null

  const operation = computed(() => store.get(unref(operationID)))

  function stopPolling() {
    if (timer) clearTimeout(timer)
    timer = null
  }

  async function refresh() {
    const current = operation.value
    if (!current || refreshing.value) return current
    refreshing.value = true
    error.value = null
    try {
      return await store.refresh(current.operation_id)
    } catch (err) {
      error.value = err
      return current
    } finally {
      refreshing.value = false
    }
  }

  async function recover() {
    const current = operation.value
    if (!current || refreshing.value) return current
    try {
      const page = await fetchRevisionEvents(cursor, {
        operationId: current.operation_id,
        limit: eventLimit
      })
      cursor = page.next_cursor
    } catch {
      // The status endpoint remains authoritative when the event cursor is lost.
    }
    return refresh()
  }

  function schedule() {
    stopPolling()
    if (!operation.value || operation.value.terminal || interval < 0) return
    timer = setTimeout(async () => {
      await recover()
      schedule()
    }, interval)
  }

  async function notifyEvent(event) {
    if (event?.operation_id !== operation.value?.operation_id) return operation.value
    return refresh()
  }

  watch(() => unref(operationID), () => {
    cursor = 0
    schedule()
  }, { immediate: true })
  watch(() => operation.value?.terminal, (terminal) => {
    if (terminal) stopPolling()
  })
  onScopeDispose(stopPolling)

  return { operation, error, refreshing, refresh, recover, notifyEvent, stopPolling }
}
