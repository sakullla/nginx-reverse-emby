import { reactive, watch, onUnmounted } from 'vue'

/**
 * Two-way sync between list filter state and URL query, per-page schema driven.
 *
 * schema: { stateKey: { key, type, baseline, values?, validate? } }
 * - key: URL query key
 * - type: 'string' | 'enum' | 'list'
 * - baseline: value treated as "no filter"; omitted from the URL
 * - values: allowed set for 'enum' (invalid values silently fall back to baseline)
 * - validate(raw): optional per-value validator ('string' and 'list' entries)
 *
 * URL -> state: precise key-level watchers (never a whole-object watchEffect,
 * see RulesPage note about preserving in-flight search text).
 * state -> URL: debounced router.replace; baseline values are omitted so
 * shareable URLs stay clean. '#id=' deep links keep living in the search key.
 */
export function useListFilterUrl({ route, router, schema, debounceMs = 300 } = {}) {
  const entries = Object.entries(schema || {})
  const values = reactive({})

  function baselineOf(def) {
    if (def.baseline !== undefined) return def.baseline
    return def.type === 'list' ? [] : ''
  }

  function isBaseline(def, value) {
    const baseline = baselineOf(def)
    if (def.type === 'list') return !Array.isArray(value) || value.length === 0
    return String(value ?? '') === String(baseline ?? '')
  }

  function parseValue(def, raw) {
    const baseline = baselineOf(def)
    if (raw === undefined || raw === null || raw === '') return baseline
    if (def.type === 'list') {
      const parts = (Array.isArray(raw) ? raw : String(raw).split(','))
        .map((item) => String(item).trim())
        .filter(Boolean)
      const validated = def.validate ? parts.filter((item) => def.validate(item)) : parts
      return validated.length ? validated : baseline
    }
    const value = Array.isArray(raw) ? String(raw[0] ?? '') : String(raw)
    if (def.type === 'enum' && Array.isArray(def.values) && !def.values.includes(value)) {
      return baseline
    }
    if (def.validate && !def.validate(value)) return baseline
    return value
  }

  function serializeValue(def, value) {
    if (isBaseline(def, value)) return undefined
    if (def.type === 'list') return value.join(',')
    return String(value)
  }

  // URL -> state: one precise watcher per schema key.
  const stopWatchers = entries.map(([stateKey, def]) =>
    watch(
      () => route.query[def.key],
      (raw) => {
        values[stateKey] = parseValue(def, raw)
      },
      { immediate: true }
    )
  )

  let timer = null
  function flushToUrl() {
    timer = null
    const nextQuery = { ...route.query }
    for (const [stateKey, def] of entries) {
      const serialized = serializeValue(def, values[stateKey])
      if (serialized === undefined) {
        delete nextQuery[def.key]
      } else {
        nextQuery[def.key] = serialized
      }
    }
    router.replace({ query: nextQuery })
  }

  function scheduleFlush() {
    if (timer) clearTimeout(timer)
    timer = setTimeout(flushToUrl, debounceMs)
  }

  /**
   * Update one filter value and sync to the URL.
   * opts.immediate skips the debounce (e.g. discrete chip/select changes).
   */
  function setValue(stateKey, value, opts = {}) {
    const def = schema[stateKey]
    if (!def) return
    values[stateKey] = def.type === 'list'
      ? (Array.isArray(value) ? value.map(String) : [])
      : String(value ?? '')
    if (opts.immediate) {
      if (timer) {
        clearTimeout(timer)
        timer = null
      }
      flushToUrl()
      return
    }
    scheduleFlush()
  }

  onUnmounted(() => {
    stopWatchers.forEach((stop) => stop())
    if (timer) clearTimeout(timer)
  })

  return { values, setValue, parseValue, serializeValue }
}
