import { ref, watch } from 'vue'

function normalizeView(value) {
  const raw = Array.isArray(value) ? value[0] : value
  const normalized = String(raw || '').trim().toLowerCase()
  return normalized === 'list' ? 'list' : 'card'
}

export function useViewToggle(pageKey) {
  const storageKey = `view:${pageKey}`
  const view = ref(normalizeView(localStorage.getItem(storageKey)))

  watch(view, (v) => {
    const normalized = normalizeView(v)
    if (normalized !== v) {
      view.value = normalized
      return
    }
    localStorage.setItem(storageKey, normalized)
  })

  return { view }
}
