import { ref } from 'vue'

function readInitialToken() {
  if (typeof localStorage === 'undefined') return null
  return localStorage.getItem('panel_token')
}

export const authToken = ref(readInitialToken())
export const sessionToken = ref(
  typeof localStorage === 'undefined' ? null : localStorage.getItem('panel_session')
)
export const credentialVersion = ref(0)

const identityChangeListeners = new Set()

function notifyIdentityChange() {
  credentialVersion.value += 1
  for (const listener of identityChangeListeners) listener()
}

export function onCredentialIdentityChange(listener) {
  identityChangeListeners.add(listener)
  return () => identityChangeListeners.delete(listener)
}

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
  const changed = authToken.value !== normalized
  authToken.value = normalized
  if (changed) notifyIdentityChange()
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

export function getStoredSessionToken() {
  if (sessionToken.value) return sessionToken.value
  if (typeof localStorage === 'undefined') return null
  return localStorage.getItem('panel_session')
}

export function setSessionToken(token) {
  const normalized = String(token || '').trim() || null
  const changed = sessionToken.value !== normalized
  sessionToken.value = normalized
  if (changed) notifyIdentityChange()
  if (typeof localStorage === 'undefined') return
  if (normalized) {
    localStorage.setItem('panel_session', normalized)
    return
  }
  localStorage.removeItem('panel_session')
}

export function clearSessionToken() {
  setSessionToken(null)
}

export function clearCredentials() {
  clearSessionToken()
  clearAuthToken()
}
