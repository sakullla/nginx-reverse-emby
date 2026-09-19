import { computed, onScopeDispose, ref, watch } from 'vue'
import { fetchRevisionEvents } from '../api/operations'
import { useOperationsStore } from '../stores/operations'

export function useOperationsStatus(options = {}) {
  const store = useOperationsStore()
  const error = ref(null)
  const refreshing = ref(false)
  const eventStatus = ref('idle')
  const interval = Number(options.pollInterval) || 2000
  const eventLimit = Number(options.eventLimit) || 500
  const fallbackEvery = Number(options.statusFallbackEvery) || 3
  const maxEventPages = Number(options.maxEventPages) || 4
  const statusConcurrency = Number(options.statusConcurrency) || 4
  let cursor = 0
  let quietPolls = 0
  let restored = false
  let timer = null

  const operations = computed(() => store.operations.value.filter((operation) => !operation.terminal))
  const operationIDs = computed(() => operations.value.map((operation) => operation.operation_id).filter(Boolean))

  function stopPolling() {
    if (timer) clearTimeout(timer)
    timer = null
  }

  async function refreshMany(ids) {
    const queue = [...new Set(ids)].filter((id) => store.get(id) && !store.get(id).terminal)
    const results = []
    let next = 0
    async function worker() {
      while (next < queue.length) {
        const id = queue[next]
        next += 1
        try {
          results.push(await store.refresh(id))
        } catch (cause) {
          error.value ||= cause
        }
      }
    }
    const workers = Array.from(
      { length: Math.min(Math.max(1, statusConcurrency), queue.length) },
      () => worker()
    )
    await Promise.all(workers)
    return results
  }

  async function recover() {
    const tracked = operationIDs.value
    if (tracked.length === 0 || refreshing.value) return []
    refreshing.value = true
    error.value = null
    const trackedSet = new Set(tracked)
    const changed = new Set()
    try {
      eventStatus.value = 'connecting'
      let pageCount = 0
      let hasMore = false
      do {
        const previousCursor = cursor
        const page = await fetchRevisionEvents(cursor, { limit: eventLimit })
        cursor = Math.max(cursor, page.next_cursor)
        hasMore = page.has_more && cursor > previousCursor
        pageCount += 1
        for (const event of page.events) {
          if (trackedSet.has(event.operation_id)) changed.add(event.operation_id)
        }
      } while (hasMore && pageCount < maxEventPages)

      eventStatus.value = 'connected'
      if (changed.size > 0) {
        quietPolls = 0
        restored = true
        return refreshMany([...changed])
      }
      quietPolls += 1
      if (!restored || quietPolls >= fallbackEvery) {
        restored = true
        quietPolls = 0
        return refreshMany(tracked)
      }
      return []
    } catch (cause) {
      eventStatus.value = 'disconnected'
      error.value = cause
      restored = true
      quietPolls = 0
      return refreshMany(tracked)
    } finally {
      refreshing.value = false
    }
  }

  function schedule() {
    stopPolling()
    if (operationIDs.value.length === 0 || interval < 0) return
    timer = setTimeout(async () => {
      await recover()
      schedule()
    }, interval)
  }

  watch(() => operationIDs.value.join('\u0000'), schedule, { immediate: true })
  onScopeDispose(stopPolling)

  return { operations, error, refreshing, eventStatus, recover, refreshMany, stopPolling }
}
