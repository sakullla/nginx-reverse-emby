import { ref, watch } from 'vue'

const cache = new Map()

function storageKey(key) {
  return `pref:${key}`
}

function readStored(key, fallback) {
  try {
    const raw = localStorage.getItem(storageKey(key))
    if (raw == null) return fallback
    return JSON.parse(raw)
  } catch {
    return fallback
  }
}

/**
 * Generic local preference store shared across callers of the same key.
 * Values are JSON-serialized under `pref:<key>` so future settings can reuse it.
 */
export function usePreference(key, defaultValue) {
  if (cache.has(key)) return cache.get(key)

  const value = ref(readStored(key, defaultValue))
  watch(
    value,
    (next) => {
      try {
        localStorage.setItem(storageKey(key), JSON.stringify(next))
      } catch {
        // ignore quota / private-mode write failures
      }
    },
    { flush: 'sync' }
  )
  cache.set(key, value)
  return value
}

/** Test helper — clears in-memory shared refs. */
export function __resetPreferenceCacheForTests() {
  cache.clear()
}
