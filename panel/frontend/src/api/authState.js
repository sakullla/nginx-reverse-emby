import { ref } from 'vue'

function readInitialToken() {
  if (typeof localStorage === 'undefined') return null
  return localStorage.getItem('panel_token')
}

export const authToken = ref(readInitialToken())

const panelTokenCookie = 'nre_panel_token'

function syncPanelTokenCookie(token) {
  if (typeof document === 'undefined') return
  if (token) {
    document.cookie = `${panelTokenCookie}=${encodeURIComponent(token)}; Path=/panel-api; SameSite=Strict`
    return
  }
  document.cookie = `${panelTokenCookie}=; Path=/panel-api; Max-Age=0; SameSite=Strict`
}

if (authToken.value) {
  syncPanelTokenCookie(authToken.value)
}

export function getStoredAuthToken() {
  if (authToken.value) return authToken.value
  if (typeof localStorage === 'undefined') return null
  return localStorage.getItem('panel_token')
}

export function setAuthToken(token) {
  const normalized = String(token || '').trim() || null
  authToken.value = normalized
  if (typeof localStorage === 'undefined') {
    syncPanelTokenCookie(normalized)
    return
  }
  if (normalized) {
    localStorage.setItem('panel_token', normalized)
    syncPanelTokenCookie(normalized)
    return
  }
  localStorage.removeItem('panel_token')
  syncPanelTokenCookie(null)
}

export function clearAuthToken() {
  setAuthToken(null)
}
