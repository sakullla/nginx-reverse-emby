import { ref } from 'vue'

function readInitialToken() {
  if (typeof localStorage === 'undefined') return null
  return localStorage.getItem('panel_token')
}

export const authToken = ref(readInitialToken())
export const sessionToken = ref(
  typeof localStorage === 'undefined' ? null : localStorage.getItem('panel_session')
)

export function getStoredAuthToken() {
  if (authToken.value) return authToken.value
  if (typeof localStorage === 'undefined') return null
  return localStorage.getItem('panel_token')
}

export function setAuthToken(token) {
  const normalized = String(token || '').trim() || null
  authToken.value = normalized
  if (typeof localStorage === 'undefined') return
  if (normalized) {
    localStorage.setItem('panel_token', normalized)
    return
  }
  localStorage.removeItem('panel_token')
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
  sessionToken.value = normalized
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
