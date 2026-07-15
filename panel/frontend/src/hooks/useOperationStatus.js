import { computed, onScopeDispose, ref, unref, watch } from 'vue'
import { fetchRevisionEvents } from '../api/operations'
import { useOperationsStore } from '../stores/operations'

export function useOperationStatus(operationID, options = {}) {
  const store = useOperationsStore()
  const error = ref(null)
  const refreshing = ref(false)
  const eventStatus = ref('idle')
  const interval = Number(options.pollInterval) || 2000
  const eventLimit = Number(options.eventLimit) || 100
  const fallbackEvery = Number(options.statusFallbackEvery) || 3
  let cursor = 0
  let quietPolls = 0
  let restored = false
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
      eventStatus.value = 'connecting'
      const page = await fetchRevisionEvents(cursor, {
        operationId: current.operation_id,
        limit: eventLimit
      })
      cursor = page.next_cursor
      eventStatus.value = 'connected'
      const events = page.events.filter((event) => event.operation_id === current.operation_id)
      if (events.length > 0) {
        quietPolls = 0
        restored = true
        return notifyEvent(events[events.length - 1])
      }
      quietPolls += 1
      if (!restored || quietPolls >= fallbackEvery) {
        restored = true
        quietPolls = 0
        return refresh()
      }
      return current
    } catch {
      eventStatus.value = 'disconnected'
      restored = true
      quietPolls = 0
      // The status endpoint remains authoritative when the event cursor is unavailable.
      return refresh()
    }
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
    quietPolls = 0
    restored = false
    eventStatus.value = 'idle'
    schedule()
  }, { immediate: true })
  watch(() => operation.value?.terminal, (terminal) => {
    if (terminal) stopPolling()
  })
  onScopeDispose(stopPolling)

  return { operation, error, refreshing, eventStatus, refresh, recover, notifyEvent, stopPolling }
}
