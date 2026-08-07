import { computed } from 'vue'
import {
  authToken,
  clearAuthToken,
  clearCredentials,
  sessionToken,
  setAuthToken,
  setSessionToken
} from '../api/authState'

export function useAuthState() {
  return {
    token: authToken,
    sessionToken,
    hasToken: computed(() => !!authToken.value || !!sessionToken.value),
    setToken: setAuthToken,
    setSessionToken,
    clearToken: clearAuthToken,
    clearCredentials
  }
}
