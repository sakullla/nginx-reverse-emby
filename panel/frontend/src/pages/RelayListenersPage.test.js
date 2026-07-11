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
  const idQuery = parseIdQuery(search)
  if (!idQuery) return listeners
  const matched = listeners.filter((listener) => String(listener.id) === idQuery.id)
  return matched.length ? matched : listeners
}

describe('RelayListenersPage deep-link #id=', () => {
  it('consumes route.query.search via parseIdQuery and displayListeners', () => {
    const page = readPage()
    expect(page).toContain("import { parseIdQuery } from '../hooks/useIdSearch'")
    expect(page).toContain('parseIdQuery(route.query.search)')
    expect(page).toContain('const displayListeners = computed')
    expect(page).toContain('v-for=\'listener in displayListeners\'')
    expect(page).toContain(":listeners='displayListeners'")
  })

  it('does not introduce a search toolbar redesign', () => {
    const page = readPage()
    expect(page).not.toContain('search-wrapper')
    expect(page).not.toContain('v-model=\'searchQuery\'')
    expect(page).not.toContain('v-model="searchQuery"')
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
