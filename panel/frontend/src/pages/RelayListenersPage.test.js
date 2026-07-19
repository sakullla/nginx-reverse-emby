// @vitest-environment node

import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { exactIdItems, parseIdQuery } from '../hooks/useIdSearch'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function readPage() {
  return fs.readFileSync(path.resolve(__dirname, 'RelayListenersPage.vue'), 'utf8')
}

/** Mirror of page displayListeners selection for pure-logic coverage. */
function selectDisplayListeners(listeners, search, resolvedMatch = null, agentFilter = 'edge-1') {
  const normalizedSearch = search == null ? '' : String(search)
  if (!parseIdQuery(normalizedSearch)) return listeners
  return exactIdItems({ search: normalizedSearch, pageItems: listeners, resolvedMatch, agentFilter })
}

describe('RelayListenersPage deep-link #id=', () => {
  it('wires route search through normalized searchQuery into displayListeners', () => {
    const page = readPage()
    expect(page).toContain('exactIdItems')
    expect(page).toContain('() => route.query.search')
    expect(page).toContain("search == null ? '' : String(search)")
    expect(page).toContain('parseIdQuery(searchQuery.value)')
    expect(page).toContain('resolvedMatch: exactRelayMatch.value')
    expect(page).toContain('const displayListeners = computed')
    expect(page).toContain('v-for=\'listener in displayListeners\'')
    expect(page).toContain(":listeners='displayListeners'")
  })

  it('normalizes null and non-string route search values before parsing', () => {
    const listeners = [
      { id: 10, name: 'a' },
      { id: 20, name: 'b' },
    ]
    expect(selectDisplayListeners(listeners, null)).toEqual(listeners)
    expect(selectDisplayListeners(listeners, undefined)).toEqual(listeners)
    expect(selectDisplayListeners(listeners, 20)).toEqual(listeners)
    expect(selectDisplayListeners(listeners, ['#id=20'])).toEqual([{ id: 20, name: 'b' }])
  })

  it('uses ResourceListFilterBar for search instead of header search-wrapper', () => {
    const page = readPage()
    expect(page).toContain('ResourceListFilterBar')
    expect(page).not.toContain('search-wrapper')
    expect(page).not.toContain('QuickAgentSelect')
    expect(page).toContain(':q="searchQuery"')
  })

  it('filters to the matching listener when search=#id= hits', () => {
    const listeners = [
      { id: 10, name: 'a' },
      { id: 20, name: 'b' },
      { id: 30, name: 'c' },
    ]
    expect(selectDisplayListeners(listeners, '#id=20')).toEqual([{ id: 20, name: 'b' }])
    expect(selectDisplayListeners(listeners, '  #id=30  ')).toEqual([{ id: 30, name: 'c' }])
  })

  it('keeps the full list for non-ID searches and leaves an unresolved ID empty', () => {
    const listeners = [
      { id: 10, name: 'a' },
      { id: 20, name: 'b' },
    ]
    expect(selectDisplayListeners(listeners, undefined)).toEqual(listeners)
    expect(selectDisplayListeners(listeners, '')).toEqual(listeners)
    expect(selectDisplayListeners(listeners, 'keyword')).toEqual(listeners)
    expect(selectDisplayListeners(listeners, '#id=999')).toEqual([])
  })

  it('materializes an exact listener found outside the loaded page', () => {
    const record = { id: 57, name: 'remote relay' }
    expect(selectDisplayListeners(
      [{ id: 1, name: 'unrelated' }],
      '#id=57',
      { agentId: 'edge-2', record },
      'edge-2'
    )).toEqual([{ ...record, agent_id: 'edge-2' }])
  })
})
