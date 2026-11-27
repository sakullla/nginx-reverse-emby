import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { useBulkSelection } from './useBulkSelection.js'

describe('useBulkSelection', () => {
  it('toggles single ids and reports count', () => {
    const sel = useBulkSelection(() => [1, 2, 3])
    sel.toggle(1)
    sel.toggle(2)
    expect(sel.selectedCount.value).toBe(2)
    expect(sel.isSelected(1)).toBe(true)
    sel.toggle(1)
    expect(sel.isSelected(1)).toBe(false)
    expect(sel.selectedList.value).toEqual([2])
  })

  it('reports indeterminate and all-selected states for the current page', () => {
    const sel = useBulkSelection(() => [1, 2, 3])
    expect(sel.isAllSelected.value).toBe(false)
    expect(sel.isIndeterminate.value).toBe(false)
    sel.toggle(2)
    expect(sel.isIndeterminate.value).toBe(true)
    expect(sel.isAllSelected.value).toBe(false)
    sel.toggle(1)
    sel.toggle(3)
    expect(sel.isAllSelected.value).toBe(true)
    expect(sel.isIndeterminate.value).toBe(false)
  })

  it('toggleAll selects the whole current page, then deselects it', () => {
    const sel = useBulkSelection(() => ['a', 'b'])
    sel.toggle('a')
    sel.toggleAll()
    expect(sel.selectedList.value.sort()).toEqual(['a', 'b'])
    expect(sel.isAllSelected.value).toBe(true)
    sel.toggleAll()
    expect(sel.selectedCount.value).toBe(0)
  })

  it('keeps selections from other pages when toggling the current page', () => {
    const page = ref([1, 2])
    const sel = useBulkSelection(page)
    sel.toggleAll()
    page.value = [3, 4]
    expect(sel.isAllSelected.value).toBe(false)
    expect(sel.isIndeterminate.value).toBe(false)
    sel.toggleAll()
    expect(sel.selectedCount.value).toBe(4)
    sel.toggleAll()
    expect(sel.selectedList.value).toEqual([1, 2])
  })

  it('clears and replaces the selection', () => {
    const sel = useBulkSelection(() => [1, 2, 3])
    sel.toggleAll()
    sel.setSelection([3])
    expect(sel.selectedList.value).toEqual([3])
    sel.clear()
    expect(sel.hasSelection.value).toBe(false)
    expect(sel.isIndeterminate.value).toBe(false)
  })

  it('treats an empty page as neither all nor partially selected', () => {
    const sel = useBulkSelection(() => [])
    sel.toggleAll()
    expect(sel.isAllSelected.value).toBe(false)
    expect(sel.selectedCount.value).toBe(0)
  })
})
