import { computed, ref, toValue } from 'vue'

/**
 * Selection state for list bulk actions.
 *
 * @param {import('vue').MaybeRefOrGetter<Array<string|number>>} [currentIds]
 *   Ids visible on the current page; `toggleAll()`, `isAllSelected` and
 *   `isIndeterminate` are evaluated against this set.
 */
export function useBulkSelection(currentIds = () => []) {
  // Replace the Set on every change so dependents re-evaluate.
  const selectedIds = ref(new Set())

  const pageIds = computed(() => {
    const ids = toValue(currentIds)
    return Array.isArray(ids) ? ids : []
  })

  const selectedCount = computed(() => selectedIds.value.size)
  const selectedList = computed(() => Array.from(selectedIds.value))
  const hasSelection = computed(() => selectedIds.value.size > 0)

  const selectedOnPage = computed(() =>
    pageIds.value.filter((id) => selectedIds.value.has(id)).length
  )

  const isAllSelected = computed(() =>
    pageIds.value.length > 0 && selectedOnPage.value === pageIds.value.length
  )

  const isIndeterminate = computed(() =>
    selectedOnPage.value > 0 && selectedOnPage.value < pageIds.value.length
  )

  function isSelected(id) {
    return selectedIds.value.has(id)
  }

  function toggle(id, force) {
    const next = new Set(selectedIds.value)
    const shouldSelect = typeof force === 'boolean' ? force : !next.has(id)
    if (shouldSelect) next.add(id)
    else next.delete(id)
    selectedIds.value = next
  }

  /** Select every id on the current page, or deselect them when all are already selected. */
  function toggleAll(ids) {
    const targets = Array.isArray(ids) ? ids : pageIds.value
    if (!targets.length) return
    const next = new Set(selectedIds.value)
    const allSelected = targets.every((id) => next.has(id))
    for (const id of targets) {
      if (allSelected) next.delete(id)
      else next.add(id)
    }
    selectedIds.value = next
  }

  /** Replace the selection, e.g. to keep only ids whose bulk action failed. */
  function setSelection(ids) {
    selectedIds.value = new Set(Array.isArray(ids) ? ids : [])
  }

  function clear() {
    if (selectedIds.value.size === 0) return
    selectedIds.value = new Set()
  }

  return {
    selectedIds,
    selectedList,
    selectedCount,
    hasSelection,
    isAllSelected,
    isIndeterminate,
    isSelected,
    toggle,
    toggleAll,
    setSelection,
    clear
  }
}
