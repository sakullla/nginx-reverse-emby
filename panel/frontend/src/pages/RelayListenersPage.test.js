import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseIdQuery } from '../hooks/useIdSearch'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function readPage() {
  return fs.readFileSync(path.resolve(__dirname, 'RelayListenersPage.vue'), 'utf8')
}

/** Mirror of page displayListeners selection for pure-logic coverage. */
function selectDisplayListeners(listeners, search) {
  const normalizedSearch = search == null ? '' : String(search)
  const idQuery = parseIdQuery(normalizedSearch)
  if (!idQuery) return listeners
  const matched = listeners.filter((listener) => String(listener.id) === idQuery.id)
  return matched.length ? matched : listeners
}

describe('RelayListenersPage deep-link #id=', () => {
  it('wires route search through normalized searchQuery into displayListeners', () => {
    const page = readPage()
    expect(page).toContain("import { parseIdQuery } from '../hooks/useIdSearch'")
    expect(page).toContain('() => route.query.search')
    expect(page).toContain("search == null ? '' : String(search)")
    expect(page).toContain('parseIdQuery(searchQuery.value)')
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

  it('keeps the full list when search is empty or #id= misses', () => {
    const listeners = [
      { id: 10, name: 'a' },
      { id: 20, name: 'b' },
    ]
    expect(selectDisplayListeners(listeners, undefined)).toEqual(listeners)
    expect(selectDisplayListeners(listeners, '')).toEqual(listeners)
    expect(selectDisplayListeners(listeners, 'keyword')).toEqual(listeners)
    expect(selectDisplayListeners(listeners, '#id=999')).toEqual(listeners)
  })
})
