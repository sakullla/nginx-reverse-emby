import { ref } from 'vue'

function readInitialToken() {
  if (typeof localStorage === 'undefined') return null
  return localStorage.getItem('panel_token')
}

export const authToken = ref(readInitialToken())

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
